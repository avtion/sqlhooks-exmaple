package example

import (
	"context"
	"database/sql"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/qustavo/sqlhooks/v2"
	"go.uber.org/zap"
)

// 覆盖驱动名 mysql 会导致 panic, 因此需要创建新的驱动.
//
// database/sql/sql.go:51
const zapDriverName = "mysql-zap"

func initZapHook(log *zap.Logger) {
	if log == nil {
		log = zap.L()
	}
	hook := &zapHook{Logger: log, IsPrintSQLDuration: true}
	sql.Register(zapDriverName, sqlhooks.Wrap(new(mysql.MySQLDriver), hook))
}

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

func (z *zapHook) OnError(_ context.Context, err error, query string, args ...interface{}) error {
	if z == nil || z.Logger == nil {
		return nil
	}
	z.With(zap.Error(err)).Error("log after err happened", buildQueryArgsFields(query, args...)...)
	return nil
}
