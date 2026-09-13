package service

import (
	"gorm.io/gorm/clause"
)

// lockForUpdate 返回 SELECT ... FOR UPDATE 锁子句。
// PostgreSQL 下为行级排他锁；SQLite（本地演示/测试驱动）会被方言安全忽略。
func lockForUpdate() clause.Expression {
	return clause.Locking{Strength: "UPDATE"}
}
