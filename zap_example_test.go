package example

import (
	"context"
	"database/sql"
	"log"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var ctx = context.Background()

var dsn = os.Getenv("DSN")

func init() {
	initZapHook(setupZapLogging(zap.InfoLevel))
	rand.Seed(time.Now().UnixNano())
}

func Example_zapHook() {
	//rawSQLExample()

	//sqlxExample()

	gormExample()
}

func Test_zapHook(t *testing.T) {
	Example_zapHook()
}

// database/sql
func rawSQLExample() {
	log.Println("going using database/sql exec sql")

	// 创建数据库连接对象
	connRaw, err := sql.Open(zapDriverName, dsn)
	if err != nil {
		log.Fatalln(err)
	}

	// 执行插入操作前需要预处理再执行
	const rawInsertSQL = `INSERT INTO test_table (score) VALUES (?)`
	stmt, err := connRaw.PrepareContext(ctx, rawInsertSQL)
	if err != nil {
		log.Fatalln(err)
	}
	if _, err = stmt.ExecContext(ctx, rand.Int63()); err != nil {
		log.Fatalln(err)
	}

	// 查询操作
	resQuery, err := connRaw.QueryContext(ctx, "SELECT * FROM test_table")
	if err != nil {
		log.Fatalln(err)
	}
	defer func() { _ = resQuery.Close() }()
	var res testTableSlice
	for resQuery.Next() {
		tmp := new(testTable)
		if err = resQuery.Scan(&tmp.ID, &tmp.Score, &tmp.CreateAt, &tmp.UpdateAt); err != nil {
			log.Fatalln(err)
		}
		res = append(res, tmp)
	}
	log.Printf("%#v", res)
}

// jmoiron/sqlx
func sqlxExample() {
	log.Println("going using jmoiron/sqlx exec sql")

	// 基于 SQLX 建立数据库链接
	connSqlx, err := sqlx.ConnectContext(ctx, zapDriverName, dsn)
	if err != nil {
		log.Fatalln(err)
	}

	// 执行插入操作前需要进行命名参数替换和预处理
	const sqlxNameInsertSQL = `INSERT INTO test_table (score) VALUES (:score)`
	var valuesToInsert = []*testTable{{Score: rand.Int63()}, {Score: rand.Int63()}, {Score: rand.Int63()}}
	newInsertSQL, args, err := sqlx.Named(sqlxNameInsertSQL, valuesToInsert)
	if err != nil {
		log.Fatalln(err)
	}
	stmt, err := connSqlx.PreparexContext(ctx, newInsertSQL)
	if err != nil {
		log.Fatalln(err)
	}
	if _, err = stmt.ExecContext(ctx, args...); err != nil {
		log.Fatalln(err)
	}

	// 查询操作可以借助 Select 方法 Scan 到对应的结构体切片
	var res testTableSlice
	if err = connSqlx.Select(&res, "SELECT * FROM test_table"); err != nil {
		log.Fatalln(err)
	}
	log.Printf("%#v", res)
}

// GORM V2, need to focus driver name is zapDriverName.
func gormExample() {
	log.Println("going using gorm V2 exec sql")

	// 创建 GORM 链接，需要注意修改 DriverName 参数
	dialector := mysql.New(mysql.Config{DSN: dsn, DriverName: zapDriverName})

	// GORM 需要开启 PrepareStmt，否则会报 driver.ErrSkip 错误
	connGorm, err := gorm.Open(dialector, &gorm.Config{PrepareStmt: true})
	if err != nil {
		log.Fatalln(err)
	}

	// 执行插入操作只需要调用 API，非常方便，但无法直观查看实际生效的 SQL
	valuesToCreate := []*testTable{{Score: rand.Int63()}, {Score: rand.Int63()}, {Score: rand.Int63()}}
	if err = connGorm.CreateInBatches(valuesToCreate, len(valuesToCreate)).Error; err != nil {
		log.Fatalln(err)
	}
	var res testTableSlice

	// 查询也是相同道理，无法直观查看实际生效的 SQL
	if err = connGorm.Find(&res).Error; err != nil {
		log.Fatalln(err)
	}
	log.Printf("%#v", res)
}
