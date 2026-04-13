package app

import (
	"fmt"
	"sync/atomic"
	"time"
)

// 全局计数器，确保同毫秒内的顺序
var hlcCounter uint32

// GenerateHLC 生成混合逻辑时钟：物理时间 + 计数器 + 节点ID前8位
func GenerateHLC(nodePubKey string) string {
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	cnt := atomic.AddUint32(&hlcCounter, 1) % 10000
	
	// 如果公钥长度不足，做个安全处理
	nodeID := "unknown"
	if len(nodePubKey) >= 8 {
		nodeID = nodePubKey[:8]
	}
	
	return fmt.Sprintf("%s_%04d_%s", ts, cnt, nodeID)
}
