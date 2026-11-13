# 服务实例元数据

`identity` 提供与部署形态无关的服务与实例元数据快照,用于日志属性和实例日志目录。它不是
认证身份,也不是雪花节点号。

```go
meta, err := identity.New(identity.Options{Service: "order", Environment: "prod"})
logger := slog.New(handler).With(meta.Attrs()...) // service/version/environment/instance_id
path, err := meta.LogPath("/var/log/svc")          // /var/log/svc/<instanceID>/service.log
```

- `Service` 与 `Environment` 必填;`Version` 缺省 `dev`;`InstanceID` 缺省使用进程内共享的
  随机值——同进程所有自动装配组件共用一个 ID。
- 显式 `InstanceID` 必须在各副本间唯一;复用同一 ID 会复用同一日志目录。
  `ValidateInstanceID` 限定可移植的单段目录名:1–128 个 ASCII 字母、数字与 `_`(首字符之后还
  允许 `-`)、以字母或数字开头;`CON`/`PRN`/`AUX`/`NUL` 与 `COM1–9`/`LPT1–9` 等 Windows 保留
  设备名在所有平台拒绝。校验错误不回显输入。
- `LogPath(root)` 只做词法拼装,不创建目录、不写文件;root 是运维信任的配置,其内容与符号链接
  不得允许不可信用户写入。
