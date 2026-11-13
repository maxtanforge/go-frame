# 标准服务命令

`Execute(ctx, args, Options)` 提供 `run`（无参数时默认）、`version`、`config check`、`config explain`。`Run`/`Check` 接收命令后面的参数，由业务决定配置文件和显式覆盖；`Check` 的第三个参数区分 explain。

包只依赖标准库。不安装信号、不修改全局 logger、不加载环境变量、不打开端口，不调用 os.Exit。版本输出是包含 version/commit/build_time/go_version 的 JSON，缺省值为 dev/unknown。非法命令不回显输入参数；回调和 writer 错误原样返回，调用方负责安全诊断及退出码。

参见 [完整消费示例](../../examples/configured/README.md)。
