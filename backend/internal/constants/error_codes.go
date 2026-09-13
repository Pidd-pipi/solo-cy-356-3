package constants

// 错误码集中维护；错误 message 由各 service/handler 手动拼接（含实体名/字段名/角色名）。
const (
	CodeOK                = 0
	CodeBadRequest        = 1000
	CodeUnauthorized      = 1001
	CodeForbidden         = 1002
	CodeNotFound          = 1003
	CodeConflict          = 1004
	CodeValidationFailed  = 1005
	CodeRateLimited       = 1006
	CodeInternalError     = 5000

	// 业务错误码
	CodePlotNotAvailable     = 2001
	CodePlanStateNotAllowed  = 2002
	CodeCropNotInSeason      = 2003
	CodePlanAlreadyCompleted = 2004
	CodeHarvestBeforeMature  = 2005
	CodePostRemoved          = 2006
	CodeDuplicateUsername    = 2007
	CodeInvalidCredentials   = 2008
	CodeUserDisabled         = 2009

	// 地块协作错误码
	CodePlotNotAdopted        = 2010 // 地块未被认养（已释放回共享池）
	CodeNotPlotOwner          = 2011 // 非认养人无权邀请/撤回
	CodeDuplicateInvitation   = 2012 // 对同一居民存在待处理邀请
	CodeAlreadyPlotMember     = 2013 // 该居民已是地块成员
	CodePlotMemberFull        = 2014 // 协作名额已满（最多 4 人）
	CodeInvitationNotPending  = 2015 // 邀请不是待处理状态
	CodeNotInvitee            = 2016 // 仅被邀请人可接受/拒绝
	CodeOwnerCannotLeave      = 2017 // 认养人不能以成员身份退出
	CodeCannotInviteSelf      = 2018 // 不能邀请自己
)

// ErrorText 错误码默认文案（service/handler 可覆盖拼接更具体的 message）
var ErrorText = map[int]string{
	CodeOK:                "ok",
	CodeBadRequest:        "请求参数错误",
	CodeUnauthorized:      "未登录或登录已过期",
	CodeForbidden:         "无权访问该资源",
	CodeNotFound:          "资源不存在",
	CodeConflict:          "资源状态冲突",
	CodeValidationFailed:  "参数校验失败",
	CodeRateLimited:       "请求过于频繁，请稍后再试",
	CodeInternalError:     "服务器内部错误",
	CodePlotNotAvailable:  "地块当前不可认养",
	CodePlanStateNotAllowed: "种植计划当前状态不允许该操作",
	CodeCropNotInSeason:   "所选作物不在当前季节推荐列表",
	CodePlanAlreadyCompleted: "种植计划已完成，无法再次操作",
	CodeHarvestBeforeMature: "作物尚未成熟，不允许记录收成",
	CodePostRemoved:       "帖子已删除",
	CodeDuplicateUsername: "用户名已被占用",
	CodeInvalidCredentials: "用户名或密码错误",
	CodeUserDisabled:      "账号已被禁用",
	CodePlotNotAdopted:    "地块未被认养（已释放回共享池），无法进行协作操作",
	CodeNotPlotOwner:      "仅地块认养人可执行该协作操作",
	CodeDuplicateInvitation: "已向该居民发送过待处理邀请，请勿重复邀请",
	CodeAlreadyPlotMember: "该居民已是本地块协作成员",
	CodePlotMemberFull:    "协作名额已满（最多 4 人）",
	CodeInvitationNotPending: "邀请已处理或已撤回，无法再次操作",
	CodeNotInvitee:        "仅被邀请居民本人可接受或拒绝邀请",
	CodeOwnerCannotLeave:  "认养人不能退出自己的地块，请先释放地块",
	CodeCannotInviteSelf:  "不能邀请自己协作地块",
}
