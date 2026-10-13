package lib

import "fmt"

// Compute 返回切片中所有元素之和；一旦出现负数元素即返回错误。
// 注意：返回值由 int 改为 (int, error)，调用方的用法需要同步调整。
func Compute(items []int) (int, error) {
	sum := 0
	for _, v := range items {
		if v < 0 {
			return 0, fmt.Errorf("negative value: %d", v)
		}
		sum += v
	}
	return sum, nil
}