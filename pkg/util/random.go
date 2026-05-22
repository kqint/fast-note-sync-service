package util

import (
	cryptorand "crypto/rand"
	"math/rand"
	"time"
)

// GenerateRandomNumber generates a slice of unique random integers within specified range
// GenerateRandomNumber 生成一组指定范围内、不重复的随机整数切片
// start: minimum value of random number
// start: 随机数的最小值
// end: maximum value of random number
// end: 随机数的最大值
// count: number of random numbers to generate
// count: 生成的随机数个数
// return: generated random number slice
// 返回值: 生成的随机数切片

func GenerateRandomNumber(start int, end int, count int) []int {
	if end < start || (end-start) < count {
		return nil
	}
	total := end - start
	// This is a shuffled sequence [0, 1, 5, 2, 4...]
	// 这是一个打乱的序列 [0, 1, 5, 2, 4...]
	perm := rand.Perm(total)

	nums := make([]int, count)
	for i := 0; i < count; i++ {
		nums[i] = perm[i] + start
	}
	return nums
}

// InArray checks whether an integer is in a slice (used for random number generation)
// InArray 检查整数是否在切片中（用于随机数生成）
// nums: integer slice
// nums: 整数切片
// num: integer to be checked
// num: 待检查的整数
// return: true if in slice, false otherwise
// 返回值: 如果在切片中返回true，否则返回false
func InArray(nums []int, num int) bool {
	for _, v := range nums {
		if v == num {
			return true
		}
	}
	return false
}

// GenerateRandomSingleNumber generates a single random number
// GenerateRandomSingleNumber 生成单个随机数
// start: minimum value of random number
// start: 随机数的最小值
// end: maximum value of random number
// end: 随机数的最大值
// return: generated random number
// 返回值: 生成的随机数
func GenerateRandomSingleNumber(start int, end int) int {
	if end < start {
		return start
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(end-start) + start
}

// GetRandomString generates random string of specified length using a
// cryptographically secure random source. This is appropriate for security
// sensitive values such as auth-token nonces and one-time passwords.
//
// GetRandomString 使用密码学安全的随机源生成指定长度的随机字符串。
// 适用于授权令牌 nonce、一次性口令等安全敏感场景。
//
// 之所以必须使用 crypto/rand 而非 math/rand：
//   - math/rand 的全局源在 Go < 1.20 默认种子为 1，相同进程的同一调用次序会
//     产生固定输出，导致每次重启后授权得到的 token 相同；
//   - 即便在 Go >= 1.20 自动播种的情况下，math/rand 输出仍然是可预测的伪随机
//     数，不应当用于签发安全令牌的随机分量。
func GetRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if length <= 0 {
		return ""
	}

	b := make([]byte, length)
	if _, err := cryptorand.Read(b); err != nil {
		// crypto/rand should never fail on supported platforms; fall back to a
		// time-seeded math/rand to keep the function usable rather than
		// returning an empty string in pathological cases.
		// 在受支持的平台上 crypto/rand 几乎不会失败；万一失败时退回到时间播种
		// 的 math/rand，保持可用性而非返回空串。
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		for i := range b {
			b[i] = charset[r.Intn(len(charset))]
		}
		return string(b)
	}

	// Map random bytes to charset. Use modulo on the underlying byte; the
	// modest bias for a 62-character charset is acceptable for nonces.
	// 把随机字节映射到 charset。对 62 字符的 charset 取模带来的偏置很小，
	// 用作 nonce 完全可接受。
	for i, x := range b {
		b[i] = charset[int(x)%len(charset)]
	}
	return string(b)
}
