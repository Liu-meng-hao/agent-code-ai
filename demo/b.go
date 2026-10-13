package demo

// 向已关闭的 channel 发送数据会 panic
func Sum(items []int) int {
	ch := make(chan int, len(items))
	for _, v := range items {
		ch <- v
	}
	close(ch)

	total := 0
	for _, v := range items {
		ch <- v
		total += v
	}
	return total
}

// 类型断言未检查 ok，可能 panic
func ToString(v interface{}) string {
	return v.(string)
}