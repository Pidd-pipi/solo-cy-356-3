package service

import (
	"math/rand"
	"sync"
	"time"

	"gorm.io/gorm/clause"
)

// lockForUpdate 返回 SELECT ... FOR UPDATE 锁子句。
// PostgreSQL 下为行级排他锁；SQLite（本地演示/测试驱动）会被方言安全忽略。
func lockForUpdate() clause.Expression {
	return clause.Locking{Strength: "UPDATE"}
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
