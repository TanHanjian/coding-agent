# Go Backend

Go 服务骨架使用 `chi` 组织路由，默认只监听 `127.0.0.1:8080`。

```powershell
go run ./cmd/server
go test ./...
```

健康检查：`GET http://127.0.0.1:8080/healthz`。可通过 `APP_ADDR` 覆盖监听地址。
