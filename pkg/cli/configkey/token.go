package configkey

// TokenExpire 会话绝对上限/小时：即使一直活跃，超过该时长也必须重新登录。
// 与 TokenIdle 是两道独立闸门——idle 管「多久没动」，expire 管「总共能活多久」。
const TokenExpire = "token.expire"

// TokenIdle 会话空闲窗口/小时：每次鉴权通过即重置会话 TTL（滑动续期），
// 空闲超过该窗口未请求即登录失效；<=0 时退化为不滑动（窗口=TokenExpire）。
const TokenIdle = "token.idle"

// TokenMultiLogin 是否允许同一账号多端同时在线。
// false 时新登录会剔出该用户此前所有会话（仅保留最新一个）。
const TokenMultiLogin = "token.multiLogin"
