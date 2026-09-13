#!/usr/bin/env bash
# 突发流量并发回归：同一地块上「接受 / 重复邀请 / 撤回 / 拒绝 / 释放」同时到达。
# 断言：
#   - 绝不出现 5xx（资源耗尽不能暴露成服务器内部错误）
#   - 非成功响应只允许：4xx 业务冲突（2010/2012/2013/2014/2015/2016）或 429 限流或 503 繁忙排队
#   - 最终状态一致：释放后地块 available、成员 0、无 pending；成员唯一且不超过 4
#
# 用法：BASE=http://localhost:29516/api/v1 BURST=200 ./scripts/burst_concurrency.sh
set -u
BASE="${BASE:-http://localhost:29516/api/v1}"
BURST="${BURST:-160}"
PASS=0; FAIL=0
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
TAG="b$$_$(date +%s)"

api() { local m="$1" p="$2" t="${3:-}" j="${4:-}"
  if [ -n "$j" ]; then
    curl -s -X "$m" "$BASE$p" -H 'Content-Type: application/json' ${t:+-H "Authorization: Bearer $t"} -d "$j"
  else
    curl -s -X "$m" "$BASE$p" -H 'Content-Type: application/json' ${t:+-H "Authorization: Bearer $t"}
  fi
}
reg() { api POST /auth/register '' "{\"username\":\"$1\",\"password\":\"${1}_pw123\",\"nickname\":\"$1\"}" >/dev/null
  api POST /auth/login '' "{\"username\":\"$1\",\"password\":\"${1}_pw123\"}" | jq -r '.data.token'; }
bad(){ echo "  ❌ $*"; FAIL=$((FAIL+1)); }
ok(){ PASS=$((PASS+1)); }

ADMIN=$(api POST /auth/login '' '{"username":"admin","password":"admin123"}' | jq -r '.data.token')
OWNER=$(reg "bowner_$TAG")

echo "== 准备 harvested 地块：owner + 3 名已接受成员（4/4）+ 5 名仅 pending 居民 =="
PID=$(api POST /plots "$ADMIN" "{\"name\":\"突发地\",\"code\":\"B-$TAG\",\"area\":10,\"soil_type\":\"loam\",\"sunlight\":\"full\",\"latitude\":31.23,\"longitude\":121.4}" | jq -r '.data.id')
api POST "/plots/$PID/adopt" "$OWNER" >/dev/null

for i in 1 2 3; do
  u="bm_${TAG}_$i"; T=$(reg "$u")
  id=$(api POST "/plots/$PID/invitations" "$OWNER" "{\"username\":\"$u\"}" | jq -r '.data.id')
  api POST "/plot-invitations/$id/accept" "$T" >/dev/null
done

# 每条 pending 邀请精确记录 id 与本人 JWT：id|username|token
: > "$TMP/pending"
for i in 1 2 3 4 5; do
  u="bp_${TAG}_$i"; T=$(reg "$u")
  id=$(api POST "/plots/$PID/invitations" "$OWNER" "{\"username\":\"$u\"}" | jq -r '.data.id')
  echo "$id|$u|$T" >> "$TMP/pending"
done

PLAN=$(api POST /planting-plans "$OWNER" "{\"plot_id\":$PID,\"crop_name\":\"番茄\",\"crop_type\":\"vegetable\",\"season\":\"summer\"}" | jq -r '.data.id')
for st in planting growing harvesting completed; do api POST "/planting-plans/$PLAN/status" "$OWNER" "{\"status\":\"$st\"}" >/dev/null; done

echo "== 突发：$BURST 个请求同时到达（每请求携带精确配对 JWT）=="
: > "$TMP/jobs"
for i in $(seq 1 "$BURST"); do
  case $((i % 5)) in
    0|1)
      line=$(sed -n "$(( (i % 5) + 1 ))p" "$TMP/pending")
      id=${line%%|*}; rest=${line#*|}; tok=${rest#*|}
      echo "POST|/plot-invitations/$id/accept|$tok|" >> "$TMP/jobs" ;;
    2)
      line=$(sed -n "$(( (i % 5) + 1 ))p" "$TMP/pending")
      u=$(echo "$line" | cut -d'|' -f2)
      echo "POST|/plots/$PID/invitations|$OWNER|{\"username\":\"$u\"}" >> "$TMP/jobs" ;;
    3)
      line=$(sed -n "$(( (i % 5) + 1 ))p" "$TMP/pending"); id=${line%%|*}
      echo "POST|/plot-invitations/$id/revoke|$OWNER|" >> "$TMP/jobs" ;;
    4)
      line=$(sed -n "$(( (i % 5) + 1 ))p" "$TMP/pending")
      id=${line%%|*}; rest=${line#*|}; tok=${rest#*|}
      echo "POST|/plot-invitations/$id/reject|$tok|" >> "$TMP/jobs" ;;
  esac
done
# 加入释放请求（不同数量，保证有释放）
for i in 1 2 3; do echo "POST|/plots/$PID/release|$OWNER|" >> "$TMP/jobs"; done

n=0
while IFS='|' read -r method path tok json; do
  n=$((n+1))
  (
    if [ -n "$json" ]; then
      curl -s -w $'\n%{http_code}' -X "$method" "$BASE$path" -H 'Content-Type: application/json' -H "Authorization: Bearer $tok" -d "$json" > "$TMP/out.$n"
    else
      curl -s -w $'\n%{http_code}' -X "$method" "$BASE$path" -H 'Content-Type: application/json' -H "Authorization: Bearer $tok" > "$TMP/out.$n"
    fi
  ) &
done < "$TMP/jobs"
wait

echo "== 结果分类 =="
declare -A HTTPCOUNT; declare -A CODECOUNT
INTERNAL=0; BUSY=0; RATELIMITED=0; SUCCESS=0; BIZ=0
for f in "$TMP"/out.*; do
  [ -s "$f" ] || continue
  h=$(tail -1 "$f"); b=$(sed '$d' "$f"); c=$(echo "$b" | jq -r '.code // -1' 2>/dev/null)
  HTTPCOUNT[$h]=$(( ${HTTPCOUNT[$h]:-0} + 1 ))
  CODECOUNT[$c]=$(( ${CODECOUNT[$c]:-0} + 1 ))
  case $h in
    200) SUCCESS=$((SUCCESS+1));;
    429) RATELIMITED=$((RATELIMITED+1));;
    503) BUSY=$((BUSY+1));;
    4??) BIZ=$((BIZ+1));;
    *) INTERNAL=$((INTERNAL+1)); echo "    非预期 HTTP $h: $(echo "$b" | head -c 220)";;
  esac
done
echo "  HTTP 分布: $(for k in "${!HTTPCOUNT[@]}"; do echo "$k=${HTTPCOUNT[$k]}"; done | sort | tr '\n' ' ')"
echo "  业务码分布: $(for k in "${!CODECOUNT[@]}"; do echo "$k=${CODECOUNT[$k]}"; done | sort -n | tr '\n' ' ')"
echo "  成功=$SUCCESS 业务冲突=$BIZ 限流429=$RATELIMITED 繁忙503=$BUSY 内部错误/异常=$INTERNAL"
[ "$INTERNAL" = "0" ] && ok || bad "突发期间出现 5xx 或异常响应"

echo "== 最终状态一致性 =="
final=$(api GET "/plots/$PID/collaboration" "$OWNER")
status=$(echo "$final" | jq -r '.data.plot.status')
members=$(echo "$final" | jq -r '.data.member_count')
pending=$(echo "$final" | jq -r '[.data.invitations[]|select(.status=="pending")]|length')
uniq=$(echo "$final" | jq -r '[.data.members[].user_id]|unique|length')
echo "  plot=$status members=$members unique=$uniq pending=$pending"
[ "$members" = "$uniq" ] || bad "成员出现重复"
[ "$members" -le 4 ] || bad "成员超过 4 人上限: $members"
if [ "$status" = "available" ]; then
  [ "$members" = "0" ] && [ "$uniq" = "0" ] && [ "$pending" = "0" ] && ok || bad "释放终态不一致 members=$members uniq=$uniq pending=$pending"
else
  bad "释放请求存在但地块未释放（status=$status）"
fi

echo
echo "================ 突发并发: PASS=$PASS FAIL=$FAIL ================"
[ "$FAIL" = "0" ]
