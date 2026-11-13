# 声明式字段校验

`validation/tags` 通过 go-playground/validator/v10 执行值规则，公开结果使用独立的 [validation.Errors](../README.md)。无全局引擎或运行时规则注册。HTTP 的请求与响应基础包不依赖第三方引擎；[binding](../../http/binding/README.md) 显式组合这些能力。

```go
type CreateInput struct {
    Name string `v:"required,min=2,max=100" label:"名称"`
    Email string `v:"required,email" label:"邮箱"`
}
rules, err := tags.Prepare(reflect.TypeFor[CreateInput](), tags.Options{
    Language: "zh",
    Messages: map[string]string{"Name.required": "请填写名称"},
})
if err != nil { return err }
err = rules.Validate(&CreateInput{Name: "Ada", Email: "ada@example.com"}, nil)
```

Prepare 在注册时检查类型、规则名、参数与适用类型，返回不可变计划。Validate 的第二个参数是请求解码提供的字段状态；传 nil 表示直接校验已有值，不具备请求缺失信息。普通字段按已赋值处理，optional.Field 仍保留自身 Present/Null。计划可并发复用，输入对象和自定义校验状态不得共享修改。

`tags.New().Validate(value)` 是一次直接校验的便利入口，使用同一 v 规则语义；重复请求优先持有 Prepared，避免重复编译。旧 `validate` 标签被拒绝，不与 v 并存。

支持 required、present、omitempty、min/max/len、gt/gte/lt/lte、oneof、email、uuid 和 dive。present 检查实际存在且非 null，允许 0/false/空串；required 保留非零值语义。d 是缺失默认值声明，不能与 present 同用；自动填默认值属于 request 的绑定阶段，直接值校验不会替调用者填值。omitempty 使用零值跳过语义，不是 PATCH 存在性判断。条件和跨字段校验写在 `Validate(*validation.Context)`，不增加组合表达式。

每个字段可适用的各项规则独立执行，收集多字段、多规则及嵌套数组错误。转换失败的字段跳过依赖有效值的规则，其他字段继续。规则遵循声明顺序，业务自定义错误按追加顺序保存。HTTP binding 合并转换与规则诊断并按 DTO 字段/数组顺序整理。

默认中文，Language 可选 zh/en；Messages 使用 Go 字段路径加规则名，例如 Name.required。覆盖值必须是开发者静态、安全消息，不插入原始输入；label 控制展示名称。自定义 Context.Add 的显式消息不受模板改写。数值范围、Unicode 字符数和集合长度使用不同提示，共享的字节长度规则应由业务纯函数实现。

递归 DTO、不支持的类型、错误规则和不合法消息返回 ErrSchema，不能统一映射为 400。规则、嵌套和校验工作量受边界限制，超过限制时整个校验失败，不返回部分集合冒充完整结果。

Options 还可设置 MaxWork（默认 65536）、MaxCustomIssues（4096）、MaxDepth（16）、MaxFields（64）、MaxRules（每字段 32）。零值选择默认值，负值为声明错误。执行前按展开后的实际对象/数组计算工作量，超出 MaxWork 或自定义 Add 预算返回 validation.ErrLimit（tags.ErrLimit 为同一错误）；它是输入限制，也匹配 ErrInvalid，不是声明错误 ErrSchema。超限不返回部分结果。Prepared.Collect 分开返回字段规则和自定义错误，供组合层维持字段/规则顺序与自定义追加顺序；普通业务使用 Validate 即可。
