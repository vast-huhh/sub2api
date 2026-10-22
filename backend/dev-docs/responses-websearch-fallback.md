# Responses → Chat Completions 的联网搜索回退

账号实际走 Responses → Chat Completions 回退且开启模拟搜索时，网关向
模型提供内部搜索工具，通过全局 WebSearch 服务执行搜索。客户端已声明的
`web_search`、`web_search_preview` 和 `web_search_preview_2025_03_11` 同样支持。

## 启用条件

- 全局「WebSearch 模拟」开启，至少配置一个可用搜索提供商。
- 账号显式 `web_search_emulation=disabled` 优先禁用。
- 账号显式 `enabled` 优先启用（仍要求全局启用）。
- 默认模式：若渠道配置了该平台的显式开关，遵循渠道；否则这条
  Responses 路径按全局配置启用。
- 未声明搜索且 `tool_choice` 未设置、为 null 或 auto 时，自动提供搜索工具，
  由模型判断是否调用；不会强制每条聊天都搜索。
- `tool_choice=none` 不执行搜索；未声明搜索时，required 或指定客户端工具
  的选择也不注入搜索，以免改变客户端的选择约束。

复用现有搜索 Manager，支持 Tavily、DeepSeek 搜索和 Brave，并沿用额度、
代理和提供商故障回退。Messages 单搜索快捷路径保持原有行为；
`/alpha/search` 的独立回退见下文。

## 执行与输出

1. 补充或转换为每个请求独有的内部搜索 function，保留其他客户端工具。
2. 请求上游 Chat Completions；收到内部调用后执行真实搜索。
3. 将结果回填为 `role=tool`，保留 assistant 的 reasoning_content 及调用 ID。
4. 继续请求模型，最终返回普通 Responses 答案和 `web_search_call` 来源 URL。

Responses 每请求最多 4 次内部网页操作（搜索、打开、查找、跟随链接共用预算），
可通过 `max_tool_calls` 降低。整体执行期限 3 分钟。
结果最多 5 条，每条摘要最多 4000 字符。`max_output_tokens` 在模型轮次间扣减。
搜索失败回填明确错误并停止继续搜索，不把失败伪装成搜索结果。
若模型同时返回内部搜索和客户端工具，执行搜索后将搜索结果作为消息上下文
随客户端工具一起返回，不在服务端执行客户端工具。

这条路径逐轮缓冲上游模型结果。`stream=true` 返回有效的 Responses SSE：
搜索进度、最终文本/工具生命周期、唯一终止事件；最终答案在该模型轮完成后发送，
目前不是逐 token 实时透传。没有启用搜索回退的请求仍使用原有流式通道。

模型用量累加所有轮次；成功的模拟搜索使用分组 `web_search_price_per_call`
计费并叠加模型费用，失败搜索不增加搜索调用计费。不复用仅供 alpha/search
使用、会替代 token 费用的 `WebSearchCalls` 字段。

当前返回来源 URL 及模型生成的引用链接，不生成字符位置级 `url_citation`
注解。支持 HTML、Markdown、纯文本网页的打开、页内查找及链接跟随；
不支持地域定位、域名过滤或仅缓存搜索。传入非空
`filters` / `user_location` 或 `external_web_access=false` 时明确报错。

## 验证

```sh
cd backend
go test -tags unit ./internal/service -run 'TestResponsesSearch|TestPrepareResponsesSearch' -count=1
go test -tags unit ./internal/pkg/apicompat ./internal/pkg/websearch
```

`TestResponsesSearchLive` 默认跳过。仅在显式设置
`SUB2API_SEARCH_LIVE_CONFIG` 指向私有 JSON 文件时调用真实上游及搜索服务。
文件含 `account`（Account 结构）和 `providers`（ProviderConfig 结构列表，
另支持 `proxy_url`）；不得提交密钥。`SUB2API_SEARCH_LIVE_OUTPUT` 可指定已存在的
目录保存响应。实测会产生模型和搜索 API 使用量。

## Codex `/alpha/search` 回退

`/alpha/search`、`/v1/alpha/search` 与 `/backend-api/codex/alpha/search`
共用同一 handler。开启全局模拟搜索后，OpenAI 兼容平台（包括 DeepSeek）
及复合分组解析到这些平台的请求，可经过正常账号调度执行模拟搜索。
API key 鉴权、分组模型限制、安全审计、余额、并发及账号/渠道开关仍然生效。
不从其他分组借用账号，不在无可用账号时绕过调度。

OpenAI 账号保留原生搜索（PAT 保留原有 Responses hosted-search 转换）。
原生入口返回 404/405/501、明确的 `unsupported_tool` / `unsupported_search` /
`unsupported_endpoint`，或 401/403 且错误码为 `no_matching_rule` 时，
才尝试模拟搜索。普通无效密钥、权限错误、额度不足、模型不存在、429 或
服务器故障不触发模拟回退。关闭模拟搜索时沿用旧行为。

模拟路径支持 `commands.search_query/open/find/click`；每请求最多 8 个操作，
其中最多 4 个搜索查询，每条查询最多
400 字符。它直接调用搜索提供商，不再调用模型。返回明文 `output` 及
`results`（`type=text_result`、引用 ID、标题、URL 和摘要），不伪造
`encrypted_output`。每条查询最多返回 5 个 HTTP(S) 来源，摘要最多 4000 字符。

当前模拟路径支持搜索、打开、查找、链接跟随；不支持其他 web.run 命令、非空
`domains`、`recency`、地域定位、域名过滤或仅缓存搜索；这些请求会明确返回
400，不会伪装成已完成。原生搜索成功时仍原样返回，不受上述限制。

每次成功的提供商查询按 `web_search_price_per_call` × 基础倍率计费；不计算
模型 token 费用。余额预留按本批最多可能执行的查询数使用相同单价。
仅一个搜索且失败时返回 502 且不计费；批量操作中失败时保留其他结果，明示失败，
只计成功查询费用，避免整批换号重放。提供商错误详情不返回客户端。

```sh
go test -tags unit ./internal/service ./internal/handler -run 'Test.*AlphaSearch|Test.*Inflight' -count=1
SUB2API_SEARCH_LIVE_CONFIG=/private/search-config.json go test -tags unit ./internal/handler -run '^TestAlphaSearchHandlerEmulationLive$' -count=1 -v
```

后一个测试使用前述私有配置，在内存中的分组/账号仓库上走完整 handler、调度、
真实搜索服务及用量记录；同时验证模拟的原生 `no_matching_rule` 错误可转接。
它不部署服务、不修改生产库，也不代表已在 Codex 界面完成端到端验证。

## Codex custom 工具兼容

Responses→Chat 支持 namespace 内的 custom 工具（如 functions.exec），
转换为带 input 字符串参数的 function，并在普通/流式响应还原 custom_tool_call
和 namespace/name；历史重放与具名 tool_choice 使用相同的摊平名。
这是独立的工具兼容修复，不依赖模型调用客户端 exec 才能执行服务端模拟搜索。

## 网页读取与引用缓存

- `open`: `ref_id` 可以是 HTTP(S) URL 或本服务返回的搜索/页面编号；可选
  `lineno`（从 0 开始），返回编号行、总行数和继续读取位置。
- `find`: `ref_id` + `pattern`，对缓存正文做大小写不敏感的字面查找，返回
  命中行上下文及是否找到。正文触及大小上限时明确提示。
- `click`: `ref_id` + `id`，读取正文中 `[link N]` 对应的 URL。它跟随链接，
  不执行网页 JavaScript，不模拟浏览器按钮。
- 批量参数在执行前完整校验；运行期间某个页面失败会输出明确错误，其余
  页面仍可成功。`response_length` 控制总文本预算（short/medium/long）。

所有网站统一通过通用网页读取接口，不设置站点专用 API 分支。提供商可选
Tavily Extract（basic）或 DeepSeek Fetch（`/v1/tools/fetch`），沿用现有密钥、
代理和额度配置，按剩余额度选取并在失败后切换；均失败后使用带大小/超时限制的
服务端 HTTP 读取。HTML 去掉脚本和常见导航噪音，Markdown 保留正文并编号链接。
Tavily 和 DeepSeek 的成功结果相互独立，不拼接两家的正文/链接，也不为补链接
调用另一家。正文缺少链接时，仅沿用当前提供商（或账号覆盖）的代理读取原网页 HTML
补充链接表；源站不可访问时仍返回已有正文，不虚构链接。只有提供商请求真正失败时
才按既有规则切换提供商。两家均可单独配置使用。
缓存版本已更新，避免继续复用旧版混合来源的快照；旧缓存按原 TTL 自然过期。
提取和直接读取沿用配置代理；不会因代理失败切换为不受配置约束的直连。
直接抓取逐跳解析并固定公网目的 IP，保留 Host/TLS SNI，阻止私网地址和 DNS 重绑定。

Redis 保存正文快照、搜索引用和链接表，TTL 1 小时，每个作用域最多 128 条。
作用域为鉴权 API Key + 客户端会话标识（优先会话请求头，其次 alpha id 或
Responses prompt_cache_key）；无会话标识的客户端按 API Key 隔离。
跨账号调度不会丢失本地引用；本地引用后续请求继续走模拟路径。
未知、过期或其他作用域的引用明确报错，建议重新打开原始 URL。

Responses→Chat 自动提供内部搜索与网页读取两个工具，共用相同读取服务。
返回 `web_search_call` 的 search/open_page/find_in_page action；内部 function
名不会发给客户端。页内查找、缓存命中及正文读取不冒充付费搜索。
当前网页读取不向下游收取新增费用；成功的 Tavily Extract / DeepSeek Fetch 每次占一个已配置
提供商操作额度（这是应用的请求计数，不等同于 Tavily credits），失败回滚额度。
模型 token 费用和真正的搜索费用保留。日志分别记录 page_fetches、extract_calls、
cache_hits、search_calls；不记录正文和密钥。

验证：

```sh
go test -tags unit ./internal/pkg/websearch ./internal/service ./internal/handler -run 'TestReader|TestTavilyExtract|TestDeepSeekFetch|TestReaderProvider|TestAlphaWeb|TestWebReader|Test.*AlphaSearch|TestResponsesSearch|TestPrepareResponsesSearch' -count=1
go test -race ./internal/pkg/websearch -run 'TestReader|TestTavilyExtract' -count=1
SUB2API_SEARCH_LIVE_CONFIG=/private/config.json go test ./internal/pkg/websearch -run '^TestWebReaderLive$' -count=1 -v
```

图片/PDF 渲染、截图、登录态网页和完整浏览器交互不在这套文本读取接口范围内。
