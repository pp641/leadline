package scoring

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgxBatch struct{ b pgx.Batch }

func (p *pgxBatch) add(sql string, args ...any) { p.b.Queue(sql, args...) }

func (p *pgxBatch) exec(ctx context.Context, pool *pgxpool.Pool) error {
	if p.b.Len() == 0 {
		return nil
	}
	return pool.SendBatch(ctx, &p.b).Close()
}
