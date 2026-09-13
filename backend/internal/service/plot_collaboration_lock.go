package service

import (
	"math/rand"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/communitygarden/server/internal/model"
)

// lockForUpdate 返回 SELECT ... FOR UPDATE 锁子句。
// PostgreSQL 下为行级排他锁；SQLite（本地演示/测试驱动）会被方言安全忽略。
func lockForUpdate() clause.Expression {
	return clause.Locking{Strength: "UPDATE"}
}

// lockPlotForUpdate 是协作写事务内的“第一把锁”（地块行）。
// 抽成包级函数便于测试注入连接池排队/锁等待超时等在进入业务校验前发生的瞬态错误。
var lockPlotForUpdate = func(tx *gorm.DB, plotID uint) error {
	var locked model.Plot
	return tx.Clauses(lockForUpdate()).First(&locked, plotID).Error
}

var (
	randMu     sync.Mutex
	globalRand = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// jitterMillis 返回 [0, base) 的随机抖动毫秒时长。
func jitterMillis(base time.Duration) time.Duration {
	randMu.Lock()
	defer randMu.Unlock()
	return time.Duration(globalRand.Int63n(int64(base)))
}
