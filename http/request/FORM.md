# 表单与流式上传

普通表单使用 `binding.Form[Req, Res](handler)`，只接受 `application/x-www-form-urlencoded` 请求体，不合并 URL Query。默认 4096 字节、64 个字段、单字段最多 1000 项；可通过 `binding.Options.Limits` 调整。字段名默认 snake_case，`form` 标签可以覆盖名称；`d/v/label`、指针布尔值、标量切片、完整错误集合与其他文本 DTO 一致。不支持嵌套对象键语法或 JSON 专用的 `optional.Field`。

```go
type Search struct {
    Current int   `d:"1" v:"gte=1"`
    Active  *bool // active=false 保留显式 false
}
```

Multipart 使用 `binding.PrepareMultipart[T]` 编译一次，再在每次请求内调用 `Decode(w, r, onFile)`。返回已校验 DTO 与完整底层错误；交给 `respond.Adapter` 时只公开第一条安全校验消息。每个请求自己的上传闭包持有临时文件列表，避免把上传状态放在共享 handler 闭包里。

```go
decoder, err := binding.PrepareMultipart[Input](binding.MultipartOptions{
    Upload: request.MultipartOptions{
        FileFields: []string{"file"}, MaxFiles: 1,
        MaxBytes: 8 << 20, MaxFileBytes: 4 << 20,
    },
})
// 在 handler 内：input, err := decoder.Decode(w, r, receiveFile)
```

底层 `request.StreamMultipart` 返回字段 `url.Values`，可独立使用。默认总请求 8 MiB、单文件 4 MiB、单个字段值 4096 字节、4 个文件、64 个字段 part、128 个总 part。DTO 累计字段字节限制配置在 `binding.MultipartOptions.Fields.Limits`，默认 4096 字节；流式限制配置在 `Upload`。

文件字段必须在 `FileFields` 中显式列出；没有声明时拒绝文件。声明是允许列表，不强制必填，必填文件由业务在 Decode 成功后检查。`AllowedMediaTypes` 只检查 MIME 声明，不证明文件内容类型；格式验证或病毒扫描在业务接收回调执行。不接受 Content-Transfer-Encoding，读取原始 part 字节。

`Upload.Reader` 仅在同步回调内有效，禁止保留或并发读取。回调少读时框架继续有界读取，仍会检查大小和截断。文件限额探测最多额外读取一个字节；回调可能收到这一个探测字节，文件必须暂存后提交。完整请求错误返回 400，限额返回 413，媒体类型错误返回 415；真实存储错误保留内部原因，不当作客户端错误。回调吞掉底层文件流错误也无法绕过检查。

框架不创建临时文件、不选择上传目录、不把文件全部读进内存。业务负责暂存、关闭和删除：后续字段、文件或校验仍可能失败，不能在文件回调中直接提交业务状态。文件名是非可信元数据，不用作磁盘路径。失败时不无界排空剩余请求，HTTP server 负责关闭 body；配置 server 读取超时，context 不能强行中断任意 Reader。

完整暂存及清理实现见 [上传示例](../../examples/protocols/upload.go)，测试覆盖校验失败和成功后的临时文件清理。
