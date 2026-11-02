// 迁移执行器：遍历已装模块的迁移链，每模块独立 goose 版本表（契约④）。
// 用法：
//
//	go run ./cmd/migrate -dsn <postgres-dsn>                 # 全部模块 up
//	go run ./cmd/migrate -dsn <dsn> -module demo             # 单模块 up
//	go run ./cmd/migrate -dsn <dsn> -down 1 -module demo     # demo 回退 1 个
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/lynxland/appkit-demo/modules"
	"github.com/lynxland/appkit/compose"
	"github.com/lynxland/appkit/goosekit"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("LYNX_START_DEMO_DATA_DATABASE_SOURCE"), "Postgres DSN（默认取 LYNX_START_DEMO_DATA_DATABASE_SOURCE）")
	module := flag.String("module", "", "只处理指定模块（空=全部）")
	down := flag.Int("down", 0, "回退最近 N 个迁移（默认 0=执行 up）")
	flag.Parse()

	if *dsn == "" {
		fatal("-dsn 或 LYNX_START_DEMO_DATA_DATABASE_SOURCE 必填")
	}
	db := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(*dsn)))
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	installed := modules.Installed()
	for _, m := range installed {
		if m.Module.Migrations == nil {
			continue
		}
		if *module != "" && m.Module.Name != *module {
			continue
		}
		var err error
		switch {
		case *down > 0:
			err = goosekit.Down(ctx, db, m.Module.Migrations, m.Module.Name, *down)
		default:
			err = goosekit.Up(ctx, db, m.Module.Migrations, m.Module.Name)
		}
		if err != nil {
			fatal(fmt.Errorf("module %s: %w", m.Module.Name, err))
		}
		v, err := goosekit.Version(ctx, db, m.Module.Migrations, m.Module.Name)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("module %-16s version %d\n", m.Module.Name, v)
	}
	_ = compose.ValidateModules(compose.Descriptors(installed))
}

func fatal(v any) {
	fmt.Fprintln(os.Stderr, "migrate:", v)
	os.Exit(1)
}
