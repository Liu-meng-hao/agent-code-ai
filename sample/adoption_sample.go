package sample

import (
	"context"
	"database/sql"
	"fmt"
)

// FindUser 演示若干典型 Go 问题，供 AI 审查验证「采纳率统计」链路使用。
func FindUser(db *sql.DB, id int64) (string, error) {
	// 问题：context.Background() 硬编码，无法传递超时/取消
	row := db.QueryRowContext(context.Background(), "SELECT name FROM users WHERE id = ?", id)

	var name string
	if err := row.Scan(&name); err != nil {
		return "", err
	}
	// run2：触发增量审查，验证历史意见采纳率统计
	return fmt.Sprintf("user:%s", name), nil
}