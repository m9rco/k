# diagnostic-logging Specification

## ADDED Requirements

### Requirement: 结构化日志写入文件
系统 SHALL 将运行日志以结构化形式（每行一条 JSON，含时间戳、级别、事件名及上下文字段）写入可配置的日志文件。系统 SHALL 支持通过配置/环境变量设置日志文件路径、最低日志级别，以及是否同时镜像输出到 stderr（开发期）。当未配置日志文件路径时，系统 SHALL 退回到仅输出 stderr，行为与引入本能力前一致。

#### Scenario: 配置日志文件后落地
- **WHEN** 配置了日志文件路径并启动服务
- **THEN** 系统将每条日志以一行 JSON 追加写入该文件
- **AND** 每条日志至少包含 `ts`、`level`、`event` 字段

#### Scenario: 开启 stderr 镜像
- **WHEN** 配置开启 stderr 镜像
- **THEN** 同一条日志同时写入日志文件与 stderr

#### Scenario: 未配置日志文件回退
- **WHEN** 未配置日志文件路径
- **THEN** 系统仅向 stderr 输出日志，不因缺省文件而报错

#### Scenario: 级别过滤
- **WHEN** 最低日志级别配置为 Info
- **THEN** 低于 Info 的 Debug 级日志不写入任何目标

### Requirement: 双层 trace 链路标识
系统 SHALL 为每一次用户消息处理（一个对话 turn）生成唯一的 `trace_id`，并在该 turn 处理期间产生的所有日志中携带该 `trace_id` 与所属 `session_id`。`trace_id` SHALL 通过请求 context 贯穿该 turn 的全部处理阶段，并在该 turn 触发的异步长任务（生图/生视频）goroutine 中继续携带，使异步任务日志可回连到触发它的 turn。

#### Scenario: 每个 turn 生成唯一 trace_id
- **WHEN** 系统开始处理一条新的用户消息
- **THEN** 系统生成一个唯一 `trace_id`
- **AND** 本 turn 内的后续日志均带有该 `trace_id` 与对应 `session_id`

#### Scenario: 同会话多 turn 可凭 session 串联
- **WHEN** 同一 session 先后处理多个 turn
- **THEN** 各 turn 拥有各自不同的 `trace_id`
- **AND** 各 turn 的日志均带有相同的 `session_id`

#### Scenario: 异步长任务回连触发 turn
- **WHEN** 某 turn 触发了异步生图/生视频任务
- **THEN** 该异步任务执行期间产生的日志携带与触发它的 turn 相同的 `trace_id`

#### Scenario: 凭 trace_id 复盘单次链路
- **WHEN** 按某个 `trace_id` 过滤日志文件
- **THEN** 可得到该 turn 从意图分类、模型调用、工具执行到异步任务的完整时间序列日志

### Requirement: 意图分类与补救决策日志
系统 SHALL 记录每个 turn 的意图分类结果、是否注入意图提示或补救提示，以及补救（remediation）决策走向（fake-exec 重试、honest-fail、clarify、refuse），用于排查工具不执行问题。

#### Scenario: 记录意图分类结果
- **WHEN** 系统对一条用户消息完成意图预分类
- **THEN** 日志记录该分类结果及是否据此注入了意图提示前缀

#### Scenario: 记录 fake-exec 自纠正
- **WHEN** 系统检测到模型假装执行（fake-exec ack）并触发重试
- **THEN** 日志记录检测到的 fake-exec、当前重试次数及本轮真实工具执行计数

#### Scenario: 记录补救分支
- **WHEN** 一个 turn 进入 clarify、refuse 或 honest-fail 补救分支
- **THEN** 日志记录所选补救分支及触发它的判定依据（如工具执行次数为零）

### Requirement: 工具调用全量入参与结果日志
系统 SHALL 记录每次工具调用的开始（含完整未截断的入参）、结束（结果摘要）与错误（完整错误信息），并在模型本轮本应调用工具却未产生任何真实工具执行时记录显式判定，用于排查工具不执行与执行异常。

#### Scenario: 记录工具开始与完整入参
- **WHEN** 一个工具开始执行
- **THEN** 日志记录工具名与完整的调用入参，且入参不被截断

#### Scenario: 记录工具结束与错误
- **WHEN** 一个工具执行结束或出错
- **THEN** 成功时日志记录工具名与结果摘要；出错时日志记录工具名与完整错误信息

#### Scenario: 记录零工具执行判定
- **WHEN** 一个 turn 结束时真实工具执行计数为零但意图分类预期应调用工具
- **THEN** 日志显式记录该"零工具执行"判定及对应意图

### Requirement: context 窗口压缩前后快照日志
系统 SHALL 在 context 窗口发生压缩时记录压缩前后的快照信息（压缩前/后消息数、折叠批次数、是否保留 tool-exchange 锚点、摘要长度），用于排查因上下文被截断导致的模型幻觉。

#### Scenario: 记录压缩快照
- **WHEN** context 窗口因超出 token 预算触发压缩
- **THEN** 日志记录压缩前消息数、压缩后消息数与本次折叠的消息批次数

#### Scenario: 记录 tool-exchange 锚点保留状态
- **WHEN** 一次压缩完成
- **THEN** 日志记录压缩后窗口是否仍包含完整的 tool-exchange 锚点

### Requirement: 模型请求与响应元数据日志
系统 SHALL 记录每次模型调用的元数据，包括 model id、流式 chunk 数、回复文本长度、是否从流式降级为 one-shot，以及调用耗时，用于关联排查幻觉与性能问题。

#### Scenario: 记录模型调用元数据
- **WHEN** 一次模型流式调用结束
- **THEN** 日志记录 model id、收到的 chunk 数、回复文本长度与调用耗时

#### Scenario: 记录降级
- **WHEN** 模型调用从流式降级为 one-shot 响应
- **THEN** 日志记录该降级事件
