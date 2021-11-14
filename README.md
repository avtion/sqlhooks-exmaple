# Go 基于原生库驱动 Driver 输出 SQL 日志 / 监控 / 链路追踪方案

<img width="1915" alt="black" src="https://user-images.githubusercontent.com/34846850/141688315-7ff4a46a-b978-48dd-8de7-51fcd6c4108e.png">

笔者曾经分享过两篇文章，分别是基于 GORM V2 和 XORM 在分布式链路追踪上的建设，此后偶尔有网友联系笔者进行交流，主要围绕项目使用 GORM V1 或者原生 SQL的情况下，在尽可能少侵入业务代码的情况下做数据库操作的日志输出、错误监控和链路追踪。

本系列文章通过四点内容为所有 Go 业务上的 SQL 操作日志输出、监控和链路追踪问题提供解决思路。

1. 利用 SQLHooks 在 sql.Driver 上挂载钩子函数。
2. ORM 、SQL 以及 SQLX 的实践。
3. Prometheus 采集 DB 操作指标。
4. Opentracing 链路追踪。

本文内容主要为第一步和第二步，后续 Prometheus 和 Opentracing 相关内容日后有机会更新，文章所用到的代码均已开源，有问题可以自行查阅，[Github - sqlhooks-example](https://github.com/avtion/sqlhooks-exmaple) 。

## SQLHooks 在 sql.Driver 上挂载钩子函数

众所周知 `database/sql` 原生库提供的是 `interface{}` 接口定义，在进行数据库操作时通常都是借助 `driver.Driver` 和 `driver.Conn`进行的，关于这部分内容可以阅读 [Go 语言设计与实现 - 数据库](https://draveness.me/golang/docs/part4-advanced/ch09-stdlib/golang-database-sql/) 内容进行了解。

既然如此，我们只需要在 Diver 和 Conn 上面封装一层就能实现全量 SQL 日志打印和监控。

在尽量避免造轮子的前提下，笔者借助 Github 开源项目 [SQLHooks](https://github.com/qustavo/sqlhooks) 进行实践。**值得注意的是，如果您需要应用到生产环境，可参考开源项目自行封装**。

SQLHooks 的原理非常简单，封装了一个 [Driver](https://github.com/qustavo/sqlhooks/blob/master/sqlhooks.go#L40) 实现原生库 `driver.Driver`，在调用 Exec、Query 以及 Prepare 等操作函数时调用开发者传入的钩子函数。

```go
// Driver implements a database/sql/driver.Driver
type Driver struct {
	driver.Driver
	hooks Hooks
}

// Hooks instances may be passed to Wrap() to define an instrumented driver
type Hooks interface {
	Before(ctx context.Context, query string, args ...interface{}) (context.Context, error)
	After(ctx context.Context, query string, args ...interface{}) (context.Context, error)
}
```

此时，有开发经验的 Gopher 已经注意到 `Hooks` 接口的方法都带有 `context.Context` 参数，大概率已经猜到下文的操作。

以打印完整 SQL 和 Args 参数为例，我们可以定义一个包括日志打印对象的结构体实现 `Hooks` 接口。

为了能更好呈现效果，本文的实践中加入了 SQL 耗时，通常该功能是在数据库中实现并呈现给 DBA 人员查询，但我们开发人员一般也需要该指标用于确定 SQL 质量。

首先我们定义 zapHook 结构体，该结构体包括一个 `zap logger` 对象和用于启用 SQL 耗时计算的布尔值。

```go
// make sure zapHook implement all sqlhooks interface.
var _ interface {
	sqlhooks.Hooks
	sqlhooks.OnErrorer
} = (*zapHook)(nil)

// zapHook using zap log sql query and args.
type zapHook struct {
	*zap.Logger

	// 是否打印 SQL 耗时
	IsPrintSQLDuration bool
}

// sqlDurationKey is context.valueCtx Key.
type sqlDurationKey struct{}
```

接下来我们需要定义 Before 函数需要做的两件事。

1. 输出实际执行 SQL 的 Query 命令和参数日志。
2. 将执行 SQL 的开始时间对象注入到上下文。

```go
func buildQueryArgsFields(query string, args ...interface{}) []zap.Field {
	if len(args) == 0 {
		return []zap.Field{zap.String("query", query)}
	}
	return []zap.Field{zap.String("query", query), zap.Any("args", args)}
}

func (z *zapHook) Before(ctx context.Context, query string, args ...interface{}) (context.Context, error) {
	if z == nil || z.Logger == nil {
		return ctx, nil
	}
	z.Info("log before sql exec", buildQueryArgsFields(query, args...)...)

	if z.IsPrintSQLDuration {
		ctx = context.WithValue(ctx, (*sqlDurationKey)(nil), time.Now())
	}
	return ctx, nil
}
```

按照相同的流程，我们需要定义 After 函数需要做的流程。

1. 尝试从上下文获取执行 SQL 的开始时间对象。
2. 输出执行 SQL 完毕的 Query 和参数日志（通常仅在 Before 函数输出一次，但本文实践为了效果进行了二次输出）。

```go
func (z *zapHook) After(ctx context.Context, query string, args ...interface{}) (context.Context, error) {
	if z == nil || z.Logger == nil {
		return ctx, nil
	}

	var durationField = zap.Skip()
	if v, ok := ctx.Value((*sqlDurationKey)(nil)).(time.Time); ok {
		durationField = zap.Duration("duration", time.Now().Sub(v))
	}

	z.With(durationField).Info("log after sql exec", buildQueryArgsFields(query, args...)...)
	return ctx, nil
}
```

我们还需要完善 `OnError` 发生错误时的钩子函数，这个函数通常是必要的，我们希望在 SQL 执行失败后进行日志输出或上报指标和原因。

```go
func (z *zapHook) OnError(_ context.Context, err error, query string, args ...interface{}) error {
	if z == nil || z.Logger == nil {
		return nil
	}
	z.With(zap.Error(err)).Error("log after err happened", buildQueryArgsFields(query, args...)...)
	return nil
}
```

至此我们已经完成了 SQLHooks 所有接口的实现，最后一步是使用 SQLHooks 库提供的 Wrap 方法创建新的 Driver 注册到全局驱动上。

```go
// 大部分 MySQL 操作都使用 go-sql-driver 作为驱动.
import (
	"database/sql"
  
	"github.com/go-sql-driver/mysql"
)

// 覆盖驱动名 mysql 会导致 panic, 因此需要创建新的驱动.
//
// database/sql/sql.go:51
const driverName = "mysql-zap"

func initZapHook(log *zap.Logger) {
	if log == nil {
		log = zap.L()
	}
	hook := &zapHook{Logger: log, IsPrintSQLDuration: true}
	sql.Register(zapDriverName, sqlhooks.Wrap(new(mysql.MySQLDriver), hook))
}
```

接下来我们就可以借助该钩子函数输出全部 DB 框架生成的 SQL 的日志。 

## 原生 SQL、 SQLX 框架以及 GORM 框架实践

在进行本文实践过程前，您需要了解本文基于以下环境进行。

- Go 1.17
- go-sql-driver v1.6.0
- SQLX 1.3.4
- GORM 1.22.3
- TiDB 5.2.2

接下来将会分成四个环节进行实践。

1. 定义测试表结构和 Go 结构体。
2. 查看 Go 原生 SQL 执行和查询效果。
3. 查看 SQLX 框架执行和查询效果。
4. 查看 GORM 框架执行和查询效果。 

### 定义测试表结构和 Go 结构体

首先笔者手动定义了一个简单的数据库表结构体并创建。

```sql
CREATE TABLE `test_table`
(
    `id`        bigint(20) NOT NULL AUTO_INCREMENT,
    `score`     bigint(20) NOT NULL,
    `create_at` timestamp  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `update_at` timestamp  NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_bin
  AUTO_INCREMENT = 30001;
```

接下来需要在 Go 代码中编写对应的结构体，此步骤可以通过各类 SQL 转 struct 工具简化。

```go
import "gorm.io/gorm/schema"

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

```

在编写业务代码之前，我们需要进行必要的初始化流程。

```go
var ctx = context.Background()

var dsn = os.Getenv("DSN")

func init() {
	initZapHook(setupZapLogging(zap.InfoLevel))
	rand.Seed(time.Now().UnixNano())
}
```

至此准备工作已经完成，接下来我们需要依照对应的框架编写业务代码，统一的流程是插入后查询。

### Go 原生库 SQL

原生库 SQL 是 Go 开发常用的数据库操作库。

因为基于原生库的批量插入需要借助字符串拼接，为了简化流程，原生库的实践仅展示单个插入的过程。

```go
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
```

编写完业务代码之后我们可以运行单元测试检验效果。

```shell
=== RUN   Test_zapHook
2021-11-14T22:00:07.255+0800	INFO	sqlhooks-exmaple/zap_example_test.go:41	going using database/sql exec sql
2021-11-14T22:00:07.256+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "INSERT INTO test_table (score) VALUES (?)", "args": [6720357018880391204]}
2021-11-14T22:00:07.261+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "4.728028ms", "query": "INSERT INTO test_table (score) VALUES (?)", "args": [6720357018880391204]}
2021-11-14T22:00:07.261+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "SELECT * FROM test_table"}
2021-11-14T22:00:07.262+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "1.098115ms", "query": "SELECT * FROM test_table"}
2021-11-14T22:00:07.262+0800	INFO	sqlhooks-exmaple/zap_example_test.go:73	- id: 30095, score: 6720357018880391204, createAt: 2021-11-14 22:00:07 +0800 CST, updateAt: 2021-11-14 22:00:07 +0800 CST
--- PASS: Test_zapHook (0.01s)
PASS
```

从日志输出来看，确实实现了我们想要的效果 —— 日志输出和 SQL 执行耗时。

### SQLX 框架

SQLX 是一款 Go 业务开发过程中比较常见的数据库操作框架，目前 Github 上有 11.1k 的 star。

得益于 SQLX 支持命名参数，且命名参数支持切片，笔者可以演示批量插入的场景。

```go
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
```

运行单元测试我们获得对应的输出效果。

```shell
=== RUN   Test_zapHook
2021-11-14T22:24:50.118+0800	INFO	sqlhooks-exmaple/zap_example_test.go:78	going using jmoiron/sqlx exec sql
2021-11-14T22:24:50.119+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "INSERT INTO test_table (score) VALUES (?),(?),(?)", "args": [415829244009450172,3465963981601780078,2197712931404613967]}
2021-11-14T22:24:50.124+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "4.85822ms", "query": "INSERT INTO test_table (score) VALUES (?),(?),(?)", "args": [415829244009450172,3465963981601780078,2197712931404613967]}
2021-11-14T22:24:50.124+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "SELECT * FROM test_table"}
2021-11-14T22:24:50.125+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "1.119153ms", "query": "SELECT * FROM test_table"}
2021-11-14T22:24:50.125+0800	INFO	sqlhooks-exmaple/zap_example_test.go:106	- id: 30096, score: 415829244009450172, createAt: 2021-11-14 22:24:50 +0800 CST, updateAt: 2021-11-14 22:24:50 +0800 CST
- id: 30097, score: 3465963981601780078, createAt: 2021-11-14 22:24:50 +0800 CST, updateAt: 2021-11-14 22:24:50 +0800 CST
- id: 30098, score: 2197712931404613967, createAt: 2021-11-14 22:24:50 +0800 CST, updateAt: 2021-11-14 22:24:50 +0800 CST
--- PASS: Test_zapHook (0.01s)
```

SQLX 框架和原生库 SQL 的输出效果是一样的，同样可以得到执行的 SQL 和耗时。

### GORM 框架

高度封装的 GORM 框架获取对应的 SQL 和执行时间难度高（特别是 V1 版本），而基于驱动的方式能磨平原生库与框架的差异，在数据库操作入口和出口捕捉我们所需要的 SQL 信息。

注意观察以下代码，我们只需要在 DB 对象创建时调整参数即能调用想要的 Hooks 钩子函数。

```go
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
```

运行单元测试，我们可以得到输出效果。

```shell
=== RUN   Test_zapHook
2021-11-14T22:43:02.364+0800	INFO	sqlhooks-exmaple/zap_example_test.go:111	going using gorm V2 exec sql
2021-11-14T22:43:02.365+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "SELECT VERSION()"}
2021-11-14T22:43:02.366+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "274.358µs", "query": "SELECT VERSION()"}
2021-11-14T22:43:02.367+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "INSERT INTO `test_table` (`score`) VALUES (?),(?),(?)", "args": [6926252270182587172,1587531254996719592,6405207277469356629]}
2021-11-14T22:43:02.370+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "3.442791ms", "query": "INSERT INTO `test_table` (`score`) VALUES (?),(?),(?)", "args": [6926252270182587172,1587531254996719592,6405207277469356629]}
2021-11-14T22:43:02.437+0800	INFO	sqlhooks-exmaple/zap.go:54	log before sql exec	{"query": "SELECT * FROM `test_table`"}
2021-11-14T22:43:02.438+0800	INFO	sqlhooks-exmaple/zap.go:72	log after sql exec	{"duration": "1.187731ms", "query": "SELECT * FROM `test_table`"}
2021-11-14T22:43:02.438+0800	INFO	sqlhooks-exmaple/zap_example_test.go:133	- id: 30099, score: 6926252270182587172, createAt: 2021-11-14 22:43:02 +0800 CST, updateAt: 2021-11-14 22:43:02 +0800 CST
- id: 30100, score: 1587531254996719592, createAt: 2021-11-14 22:43:02 +0800 CST, updateAt: 2021-11-14 22:43:02 +0800 CST
- id: 30101, score: 6405207277469356629, createAt: 2021-11-14 22:43:02 +0800 CST, updateAt: 2021-11-14 22:43:02 +0800 CST
--- PASS: Test_zapHook (0.07s)
PASS
```

日志输出的效果和预期相同，都能正常输出实际执行的 SQL 和耗时。

值得注意的是，尽管我们没有编写额外的内容，但 GORM 框架依然在初始化过程执行了 `SELECT VERSION()` 命令。

## 注意事项

笔者认为本文的内容比较基础，能给部分有需求的同学提供解决问题的思路，但仍有需要注意的事项。

- 本文基于 SQLHooks 开源库进行编写，值得注意的是，在生产环境下，基建工程尽量自行封装或者提供配置开关。
- 输出还是采集都是开销较大的工作，可提供参数关闭或者调整采集概率。
- 在条件允许的情况下，核心业务减少或避免使用 ORM，降低人为误操的风险。

非常感谢您的阅读，如果您有更好的想法或问题，欢迎私信笔者。

## 参考资料

- [Github - sqlhooks-example](https://github.com/avtion/sqlhooks-exmaple)
- [Github - SQLHooks](https://github.com/qustavo/sqlhooks)
- [Go 语言设计与实现 - 数据库](https://draveness.me/golang/docs/part4-advanced/ch09-stdlib/golang-database-sql)
- [Github - SQLX](https://github.com/jmoiron/sqlx)
- [GORM](https://gorm.io)
