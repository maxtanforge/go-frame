# 安全校验与完整错误集合

`validation` 只依赖标准库。它提供公开诊断 `Issue{Path, Code, Message}`、有序集合 `Errors` 和每次校验独立的 `Context`，不依赖 HTTP 或 Ent。

`errors.Is(err, validation.ErrInvalid)` 识别输入错误；`errors.As(err, &all)` 提取 `*validation.Errors`，通过 `all.Issues()` 读取独立快照。Path 仅接受声明的字段名和非负数组索引，例如 `validation.Path{"items", 0, "name"}`；空路径表示请求整体错误。Code 使用稳定类别，例如 required、type、min、max、length、format、enum、validation。不得把未知输入键、用户值、SQL 或内部错误文本放入公开诊断。

## 自定义校验

```go
type CreateInput struct {
    Name string `v:"required" label:"名称"`
    TaxID string
}

func (v *CreateInput) Validate(c *validation.Context) {
    if c.Decoded("TaxID") && v.TaxID != "" && c.Decoded("Name") && v.Name == "" {
        c.Add("Name", "required", "填写税号时请提供名称")
    }
}
```

字段参数使用 Go 字段路径，Context 将它转换为外部名称；不存在的字段、非法代码或消息属于 ErrSchema。Context 不是标准库 context.Context，不执行后台工作，不跨请求共享。Present/Null 表示原始存在性，Decoded 表示转换成功，Valid 表示转换成功且尚无该字段错误。默认值可以令 Decoded 为 true，但不会改变 Present。规则依赖多个字段时先检查 Decoded 或 Valid，避免拿转换失败后的零值参与业务判断。

标签规则之后仍调用自定义方法，无关字段失败不会阻止其他自定义检查；嵌套方法只对成功绑定的对象执行。跨字段条件直接写 Go，不提供 required_if、AND/OR 等条件 DSL。授权、数据库唯一性检查和其他 I/O 放在服务层。

可复用的纯值函数返回相对路径的集合：

```go
func Name(value string) error {
    if value == "" {
        return validation.NewErrors(validation.Issue{Code: "required", Message: "名称不能为空"})
    }
    return nil
}
// 在 DTO 的 Validate 中：
validation.Check(c, "Name", input.Name, Name)
```

Check 只对成功解码且非 null 的值执行规则，把相对 Issue 绑定到该字段。普通内部 error 保留为内部错误，不调用 Error() 生成公开消息。同一函数可直接用于 Ent 的 `.Validate(Name)`；不要同时重复声明同一限制的标签。字节数、Unicode 字符数与数据库列容量应分别确认。

## HTTP 与直接校验

使用 [tags.Prepare](tags/README.md) 可脱离 HTTP 校验业务构造的值；使用 [binding](../http/binding/README.md) 可自动保留请求存在性、默认值、类型转换和规则错误。直接值校验无法恢复请求中未传的信息，不能代替请求绑定。

底层保留全部可适用的错误，HTTP 仅取首条安全消息，data 为 null：

```json
{"code":400,"data":null,"message":"名称不能为空","success":false,"timestamp":1790000000000,"traceId":"..."}
```

没有安全消息时使用“请求参数不正确”。适配器不修改原集合；业务和测试仍可读取完整 Issues。语法损坏、资源超限、取消和内部错误不承诺完整字段集合。ErrSchema 是开发者声明错误，应返回 500；超时保留 504；显式 httperror.Error.Err 中的集合不会提升为公开内容。

低层解码仍可能返回单个 FieldError，它不代表完整聚合，也不再要求 HTTP 输出 data.fields。完整声明式诊断使用 Errors。Message 是开发者明确公开的内容，结构检查不能判断其中是否包含秘密。
