#!/usr/bin/env bash
# 地块协作并发缺陷回归验证（纯 HTTP，可重复执行）。
#
# 覆盖：
#   A. 同时「接受邀请」与「重复邀请同一人」——恰一方成功，另一方得到明确业务冲突(2012/2013)，无 500，成员不增多余
#   B. 5 个不同邀请同时接受——owner 占 1 席，恰 3 成功 2 满员(2014)，成员恒为 4，无重复成员
#   C. 同一邀请被处理两次——恰 1 成功，另 1 返回 2015，成员只有 1 行
#   D. 撤回 vs 接受——必有一方成功，另一方 2015，状态与结果一致
#   E. 释放 vs 接受——释放恒成功；接受先赢则随后被清理，后赢则 2010；最终 available/0 成员/0 待处理
#
# 用法：
#   BASE=http://localhost:29516/api/v1 ADMIN_PW=admin123 ./scripts/concurrent_e2e.sh
# 前置：存在管理员 admin（种子账号）。脚本会自行注册认养人与居民账号并创建独立地块，可重复运行。
set -u
BASE="${BASE:-http://localhost:29516/api/v1}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PW="${ADMIN_PW:-admin123}"
PASS=0; FAIL=0
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
TAG="c$$_$(date +%s)"

api() { # method path token [json]
  local method="$1" path="$2" token="${3:-}" json="${4:-}"
  if [ -n "$json" ]; then
    curl -s -X "$method" "$BASE$path" -H 'Content-Type: application/json' ${token:+-H "Authorization: Bearer $token"} -d "$json"
  else
    curl -s -X "$method" "$BASE$path" ${token:+-H "Authorization: Bearer $token"}
  fi
}
jcode() { jq -r '.code // empty'; }
collab_field() { # pid token jqfilter
  api GET "/plots/$1/collaboration" "$2" | jq -r "$3"
}
register() { # username -> token
  api POST /auth/register '' "{\"username\":\"$1\",\"password\":\"${1}_pw123\",\"nickname\":\"$1\"}" >/dev/null
  api POST /auth/login '' "{\"username\":\"$1\",\"password\":\"${1}_pw123\"}" | jq -r '.data.token'
}
create_plot() { # admin_token code -> id
  api POST /plots "$1" "{\"name\":\"并发地$2\",\"code\":\"$2\",\"area\":10,\"soil_type\":\"loam\",\"sunlight\":\"full\",\"latitude\":31.23,\"longitude\":121.47}" | jq -r '.data.id'
}
fail() { echo "  ❌ $*"; FAIL=$((FAIL+1)); }
expect() { if eval "$1"; then PASS=$((PASS+1)); else fail "$2"; fi
}

ADMIN=$(api POST /auth/login '' "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PW\"}" | jq -r '.data.token')
[ -n "$ADMIN" ] && [ "$ADMIN" != "null" ] || { echo "管理员登录失败（设置 ADMIN_USER/ADMIN_PW）"; exit 2; }
OWNER=$(register "owner_$TAG")

echo "== A. 同时接受 与 重复邀请（15 轮）=="
for r in $(seq 1 15); do
  pid=$(create_plot "$ADMIN" "CA-$TAG-$r")
  api POST "/plots/$pid/adopt" "$OWNER" >/dev/null
  u="ua_${TAG}_$r"; T_U=$(register "$u")
  inv=$(api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" | jq -r '.data.id')
  ( api POST "/plot-invitations/$inv/accept" "$T_U" > "$TMP/a_$r" ) &
  ( api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" > "$TMP/i_$r" ) &
  wait
  ca=$(jcode < "$TMP/a_$r"); ci=$(jcode < "$TMP/i_$r")
  expect "[ '$ca' = 0 ]" "A#$r 接受方应成功，code=$ca"
  expect "[ '$ci' = 2012 -o '$ci' = 2013 ]" "A#$r 重复方应为2012/2013，code=$ci"
  expect "[ '$ca' != 5000 -a '$ci' != 5000 ]" "A#$r 出现内部错误 ca=$ca ci=$ci"
  m=$(collab_field "$pid" "$OWNER" '.data.member_count'); expect "[ '$m' = 2 ]" "A#$r 成员应为2，实际$m"
  pc=$(collab_field "$pid" "$OWNER" '[.data.invitations[]|select(.status=="pending")]|length'); expect "[ '$pc' = 0 ]" "A#$r 待处理应为0，实际$pc"
done

echo "== B. 5 个不同邀请同时接受（10 轮，应 3 成功 / 2 满员，恒 4 成员）=="
for r in $(seq 1 10); do
  pid=$(create_plot "$ADMIN" "CB-$TAG-$r")
  api POST "/plots/$pid/adopt" "$OWNER" >/dev/null
  toks=()
  for i in 1 2 3 4 5; do
    u="ub_${TAG}_${r}_$i"; toks+=("$(register "$u")")
    api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" >/dev/null
  done
  mapfile -t invs < <(collab_field "$pid" "$OWNER" '.data.invitations|sort_by(.id)|.[].id|tostring')
  for i in 0 1 2 3 4; do
    ( api POST "/plot-invitations/${invs[$i]}/accept" "${toks[$i]}" > "$TMP/b_${r}_$i" ) &
  done
  wait
  ok=0; full=0
  for i in 0 1 2 3 4; do c=$(jcode < "$TMP/b_${r}_$i"); [ "$c" = "0" ] && ok=$((ok+1)); [ "$c" = "2014" ] && full=$((full+1)); [ "$c" = "5000" ] && fail "B#$r 第$i 个内部错误"; done
  expect "[ '$ok' = 3 ]" "B#$r 成功应为3，实际$ok"
  expect "[ '$full' = 2 ]" "B#$r 满员应为2，实际$full"
  m=$(collab_field "$pid" "$OWNER" '.data.member_count'); expect "[ '$m' = 4 ]" "B#$r 成员应为4，实际$m"
  uniq=$(collab_field "$pid" "$OWNER" '[.data.members[].user_id]|length')
  uniqall=$(collab_field "$pid" "$OWNER" '[.data.members[].user_id]|unique|length')
  expect "[ '$uniq' = '$uniqall' ]" "B#$r 出现重复成员行"
  pend=$(collab_field "$pid" "$OWNER" '[.data.invitations[]|select(.status=="pending")]|length'); expect "[ '$pend' = 2 ]" "B#$r 未接受邀请应保持pending=2，实际$pend"
done

echo "== C. 同一邀请被处理两次（15 轮，1 成功 + 1 个 2015）=="
for r in $(seq 1 15); do
  pid=$(create_plot "$ADMIN" "CC-$TAG-$r")
  api POST "/plots/$pid/adopt" "$OWNER" >/dev/null
  u="uc_${TAG}_$r"; T_U=$(register "$u")
  inv=$(api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" | jq -r '.data.id')
  ( api POST "/plot-invitations/$inv/accept" "$T_U" > "$TMP/c1_$r" ) &
  ( api POST "/plot-invitations/$inv/accept" "$T_U" > "$TMP/c2_$r" ) &
  wait
  z=0; np=0
  for f in "$TMP/c1_$r" "$TMP/c2_$r"; do c=$(jcode < "$f"); [ "$c" = "0" ] && z=$((z+1)); [ "$c" = "2015" ] && np=$((np+1)); [ "$c" = "5000" ] && fail "C#$r 内部错误"; done
  expect "[ '$z' = 1 -a '$np' = 1 ]" "C#$r 应1成功+1个2015，实际 z=$z np=$np"
  m=$(collab_field "$pid" "$OWNER" '[.data.members[]|select(.user.username=="'"$u"'")]|length'); expect "[ '$m' = 1 ]" "C#$r 该居民成员行应为1，实际$m"
done

echo "== D. 撤回 vs 接受（15 轮，恰一方成功，另一方 2015）=="
for r in $(seq 1 15); do
  pid=$(create_plot "$ADMIN" "CD-$TAG-$r")
  api POST "/plots/$pid/adopt" "$OWNER" >/dev/null
  u="ud_${TAG}_$r"; T_U=$(register "$u")
  inv=$(api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" | jq -r '.data.id')
  ( api POST "/plot-invitations/$inv/accept" "$T_U" > "$TMP/da_$r" ) &
  ( api POST "/plot-invitations/$inv/revoke" "$OWNER" > "$TMP/dr_$r" ) &
  wait
  ca=$(jcode < "$TMP/da_$r"); cr=$(jcode < "$TMP/dr_$r")
  expect "[ '$ca' != 5000 -a '$cr' != 5000 ]" "D#$r 内部错误 ca=$ca cr=$cr"
  st=$(collab_field "$pid" "$OWNER" ".data.invitations[]|select(.id==$inv)|.status")
  if [ "$ca" = "0" ]; then expect "[ '$cr' = 2015 -a '$st' = accepted ]" "D#$r 接受应胜出 ca=$ca cr=$cr st=$st"
  elif [ "$cr" = "0" ]; then expect "[ '$ca' = 2015 -a '$st' = revoked ]" "D#$r 撤回应胜出 ca=$ca cr=$cr st=$st"
  else fail "D#$r 无一方成功 ca=$ca cr=$cr"; fi
done

echo "== E. 释放 vs 接受（10 轮，释放恒成功，最终 available/0/0，释放后操作 2010）=="
# 地块需先经种植计划状态机进入 harvested（待释放）才能释放，全程纯 HTTP 制造。
for r in $(seq 1 10); do
  pid=$(create_plot "$ADMIN" "CE2-$TAG-$r")
  api POST "/plots/$pid/adopt" "$OWNER" >/dev/null
  u="uf_${TAG}_$r"; T_U=$(register "$u")
  inv=$(api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" | jq -r '.data.id')
  plan=$(api POST /planting-plans "$OWNER" "{\"plot_id\":$pid,\"crop_name\":\"番茄\",\"crop_type\":\"vegetable\",\"season\":\"summer\"}" | jq -r '.data.id')
  for st in planting growing harvesting completed; do
    api POST "/planting-plans/$plan/status" "$OWNER" "{\"status\":\"$st\"}" >/dev/null
  done
  ( api POST "/plot-invitations/$inv/accept" "$T_U" > "$TMP/ea_$r" ) &
  ( api POST "/plots/$pid/release" "$OWNER" > "$TMP/er_$r" ) &
  wait
  ca=$(jcode < "$TMP/ea_$r"); cr=$(jcode < "$TMP/er_$r")
  expect "[ '$cr' = 0 ]" "E#$r 释放应成功，code=$cr"
  if [ "$ca" != "0" ]; then expect "[ '$ca' = 2010 ]" "E#$r 接受落后应为2010，code=$ca"; fi
  ps=$(api GET "/plots/$pid" "$OWNER" | jq -r '.data.status')
  m=$(collab_field "$pid" "$OWNER" '.data.member_count')
  pend=$(collab_field "$pid" "$OWNER" '[.data.invitations[]|select(.status=="pending")]|length')
  expect "[ '$ps' = available -a '$m' = 0 -a '$pend' = 0 ]" "E#$r 终态错误 status=$ps members=$m pending=$pend"
  ra=$(api POST "/plot-invitations/$inv/accept" "$T_U" | jcode)
  ri=$(api POST "/plots/$pid/invitations" "$OWNER" "{\"username\":\"$u\"}" | jcode)
  expect "[ '$ra' = 2010 -a '$ri' = 2010 ]" "E#$r 释放后应2010，实际 accept=$ra invite=$ri"
done

echo
echo "================ 并发回归: PASS=$PASS FAIL=$FAIL ================"
[ "$FAIL" = "0" ]
