// Package syncgate provides transient per-vault sync pause gates used during
// maintenance operations such as backup import, so that concurrent multi-device
// WebSocket sync traffic cannot interleave with the import writes.
// Package syncgate 提供按仓库的临时同步暂停开关，用于备份导入等维护操作，
// 防止多端 WebSocket 同步流量与导入写入交叉导致数据错乱。
package syncgate

import (
	"fmt"
	"sync"
	"time"
)

var gates sync.Map // key: "<uid>:<vault>" -> time.Time (paused at)

// Key builds the internal gate key for a user's vault.
// Key 构造用户仓库对应的内部开关键。
func Key(uid int64, vault string) string {
	return fmt.Sprintf("%d:%s", uid, vault)
}

// Pause blocks sync message processing for the given user's vault until Resume.
// Pause 暂停指定用户仓库的同步消息处理，直到调用 Resume。
func Pause(uid int64, vault string) {
	gates.Store(Key(uid, vault), time.Now())
}

// Resume unblocks sync processing for the given user's vault.
// Resume 恢复指定用户仓库的同步消息处理。
func Resume(uid int64, vault string) {
	gates.Delete(Key(uid, vault))
}

// IsPaused reports whether sync for the user's vault is currently paused.
// IsPaused 报告该用户的仓库当前是否处于同步暂停状态。
func IsPaused(uid int64, vault string) bool {
	_, ok := gates.Load(Key(uid, vault))
	return ok
}
