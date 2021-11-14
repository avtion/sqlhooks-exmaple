package example

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm/schema"
)

// 实现 GORM 表名接口
var _ schema.Tabler = (*testTable)(nil)

type testTable struct {
	ID       uint      `db:"id"`
	Score    int64     `db:"score"`
	CreateAt time.Time `db:"create_at" gorm:"<-:false"`
	UpdateAt time.Time `db:"update_at" gorm:"<-:false"`
}

func (*testTable) TableName() string { return "test_table" }

type testTableSlice []*testTable

// GoString 输出人类看得懂的日志
func (t testTableSlice) GoString() string {
	var output strings.Builder
	for _, v := range t {
		output.WriteString(fmt.Sprintf("- id: %d, score: %d, createAt: %s, updateAt: %s\n", v.ID, v.Score, v.CreateAt.String(), v.UpdateAt.String()))
	}
	return output.String()
}
