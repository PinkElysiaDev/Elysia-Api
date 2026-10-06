package storage

import (
	"context"
	"crypto/subtle"
)

// findAPIToken 按明文 token 查找。token 以随机 nonce 加密存储，无法用 SQL
// 等值查询，只能遍历解密后比对。生产热路径由内存缓存（持解密后的 token）
// 承担，本函数无生产调用方，仅测试断言解密往返与命中语义。
func findAPIToken(store *Store, ctx context.Context, token string) (APIToken, bool, error) {
	items, err := store.ListAPITokens(ctx)
	if err != nil {
		return APIToken{}, false, err
	}
	for _, item := range items {
		if item.Enabled && subtleConstantTimeEqual(item.Token, token) {
			return item, true, nil
		}
	}
	return APIToken{}, false, nil
}

// subtleConstantTimeEqual 以常量时间比较两个字符串，避免 token 比对的时序侧信道。
func subtleConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
