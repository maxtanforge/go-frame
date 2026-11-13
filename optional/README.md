# JSON 请求三态字段

`optional.Field[T]` 用于 PATCH 等请求输入，区别缺失、显式 null 和具体值：

| JSON | Present | Null | Value |
| --- | --- | --- | --- |
| 属性缺失 | false | false | T 的零值 |
| `null` | true | true | T 的零值 |
| `false` / `0` / `""` | true | false | 对应零值 |

```go
type UpdateInput struct {
    Name optional.Field[string] `json:"name"`
    Active optional.Field[bool] `json:"active"`
}
input, err := request.DecodeJSON[UpdateInput](w, r)
// 处理 err 后，由业务检查 Present / Null，并构建显式更新 map。
```

每次请求都解码到新的 DTO；encoding/json 不访问缺失字段，因此复用已有 DTO
不会自动清除缺失属性的旧状态。解码失败会清空被访问字段，不能把整个失败请求当作成功输入。
内部 JSON decoder 拒绝未知结构体字段及多个 JSON 值，保留 `request.DecodeJSON` 的严格字段行为。
与 encoding/json 一样，业务自定义 `UnmarshalJSON` 类型自行负责内部校验。

本包只处理输入 presence，不提供 sql.Scanner、ORM 自动持久化或自动更新逻辑。
MarshalJSON 明确拒绝将 Field 用作响应，防止输出 Present/Null/Value 内部结构。
响应 VO 需显式选择普通值或可空值；请求缺失语义不能直接作为响应表示。
null 是否允许、哪些字段可更新、如何转换成数据库 NULL 都由业务决定。
