# appkit-demo

基于 [appkit](https://github.com/lynxland/appkit) 与 [lynx-mods](https://github.com/lynxland/lynx-mods) 模块基建的 Go 应用：
lynx 运行时 + grpcapi 双面（gRPC + HTTP gateway）+ bun/Postgres + goose 迁移。

## 已装模块

见 `lynx-mods.json`（安装清单）与 `modules/modules.go`（装配登记）。

## 快速开始

```bash
mise install          # 工具链（go / buf）
mise run db:up        # 本地 Postgres
cp .env.example .env
go mod tidy
mise run generate     # proto → genproto/
mise run db:migrate   # 各模块迁移链（独立版本表）
mise run dev          # gRPC :9060 / HTTP :8080
```

## 添加模块

```bash
lynx-mods add <module>
```

## 配置

`configs/config.yaml` + 环境变量覆盖（前缀 `LYNX_START_DEMO_`）：
如 `data.database.source` → `LYNX_START_DEMO_DATA_DATABASE_SOURCE`。

## 迁移

```bash
mise run db:migrate                              # 全部模块 up
go run ./cmd/migrate -dsn <dsn> -module demo     # 单模块
go run ./cmd/migrate -dsn <dsn> -down 1 -module demo
```
