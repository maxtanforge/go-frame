# 公共 Offset 分页

`pagination` 仅依赖标准库，不绑定 HTTP、SQL 驱动或业务实体。

```go
window, err := pagination.NewWindow(1, 20, pagination.DefaultLimits())
// 必须处理 err；Current/Size 为正数，调用方仅对未提供的输入填默认值。
```

默认政策：每页最多 100 条，偏移最多 1,000,000（包含边界）。业务可以显式传入 `Limits`，它不是客户端请求参数。大小和偏移越界、无效政策、零值 Window 都会返回 `ErrInvalid`。不自动钳制非法输入，先检查除法边界再计算偏移，给多查一条预留整数空间。

排序用 `Sort{Field: "title", Desc: true}` 表达公开字段别名；`ValidateSort` 拒绝未知字段、重复字段、空字段及超过 8 项的排序。允许空排序，由存储层补稳定的唯一排序。SQL 方言和列映射见 [database/query](../database/query/README.md)。

## 普通分页与无总数查询

```go
// COUNT 查询得到 total，列表最多查询 window.Size() 条。
page, err := pagination.NewPage(rows, window, total) // total 为 int64
// 不执行 COUNT 的独立接口：列表查询 window.FetchSize()（size+1）条。
slice, err := pagination.NewSlice(rows, window)
```

`Page` 固定包含五个字段：`current`、`size`、`total`、`pages`、`records`。
总数必须为非负整数；pages 向上取整，总数为 0 时 pages 为 0，空记录输出 `[]`。
不存在 `has_more` 或 null 总数。框架不自动执行 count，也不保证独立 count/list
共享数据库快照；需要一致性时由业务明确选择事务隔离和查询方案。

`Slice` 是独立的无总数结果：`current`、`size`、`has_more`、`records`，没有 total/pages。
它最多接受 size+1 条，自动去掉探测行。调用方必须实际多查一条，才能用
`has_more=false` 判断结束；单纯查 size 条不能证明结束。records 示例采用此类型。
它仍是 Offset 分页，不是游标分页。

两种构造器都会复制记录切片，记录对象中的引用字段仍由调用方负责所有权。
HTTP 层直接使用 `pagination.Page` 和 `pagination.NewPage(rows, window, total)`；
分页能力由 `pagination` 提供。
非法参数返回 ErrInvalid，由 HTTP 边界映射为 400。
Offset 在并发插入/删除时可能出现跨页重复或遗漏，唯一排序只消除同值排序的不确定顺序。大偏移有查询成本，因此政策设上限；游标分页另行迭代。
