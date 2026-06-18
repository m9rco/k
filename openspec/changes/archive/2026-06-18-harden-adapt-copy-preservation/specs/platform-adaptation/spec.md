## MODIFIED Requirements

### Requirement: 适配后质量打分门控与单次重生

系统 SHALL 在每个 **AI 重绘**适配产物收敛到目标尺寸后、持久化前，对其执行一次质量打分审核（**仅 AI 重绘路径**，确定性裁剪快路径不触发）。审核 SHALL 由独立的视觉判官模型完成，默认为 `doubao-seed-1-6-vision-250815`（inline base64，无需公网 URL）。判官 prompt MUST 为服务端固定文案，携带目标渠道/尺寸、**placement 文案约定**（`SizeNote`，如「无文案」「仅 logo」）与（如有）宣发主题报告。

判官 SHALL 评估六个维度：合规性（`compliance`，红线）、主体一致性（`subject_consistency`）、人物卖相（`character_appeal`）、整体质量（`overall_quality`）、画面完整度（`canvas_fill`）、**必备要素保真（`key_elements_fidelity`，0-100）**。`key_elements_fidelity` SHALL 依据宣发主题报告「必须保留」清单逐项核对：核心主体/LOGO 是否在画面内、要求保留的文字是否存在且字符正确（非糊化/改写/乱码）。

必保清单 SHALL 按 placement 文案约定过滤：核心主体/LOGO 在任何 placement 均 MUST 必保；纯文案类要素（定档大字、底部标签等）仅当 placement **未**约定「无文案」时才纳入必保。

判官生成改进 `hints` 时 SHALL 同样遵守过滤规则：**若 placement 约定「无文案」，hints SHALL NOT 建议补充纯文案要素**（如「补全定档大字」「补齐标签」）；若约定「仅 logo」，hints 不建议补充纯文案，仅可提 LOGO。该过滤消除 REVISE 与 placement 约定的矛盾指令。

判定 MUST 由服务端依据结构化结果作出，SHALL NOT 直接采信模型自报的及格/不及格结论：
- **合规性为硬红线**：命中违禁内容时，无论其余分数，直接判不及格（一票否决）。
- **必备要素保真为硬红线**：`key_elements_fidelity` 低于 `KEY_ELEMENTS_FIDELITY_MIN`（缺省 60）时，无论加权总分，直接判不及格。**不被其余高分维度的加权总分掩盖**。
- **画面完整度为硬红线**：`canvas_fill` 低于下限（缺省 60）时直接判不及格。
- 以上红线通过时，以主体一致性、人物卖相、整体质量的加权总分与阈值（缺省 75）比较。

当首检（`Attempt=0`）不及格时，系统 SHALL 把原因与改进 hints（已按 placement 过滤）反馈给 `gpt-image-2`，重走一遍完整生图流程（`Attempt=1`）。重生 SHALL 复用同一任务标识。

重生产物（`Attempt=1`）SHALL **再经判官打分一次**（仅这一次，SHALL NOT 触发第三轮生图）。系统 SHALL 在首检版与重生版之间**按红线感知优先级择优**持久化，比较顺序为：(1) 红线通过性——`Pass=true` 优先于 `Pass=false`；(2) 红线状态相同时比 `key_elements_fidelity`，高者优先；(3) `key_elements_fidelity` 相同时比加权 `total`，高者优先；(4) 仍相等时取重生版（已含改进 hints）。系统 SHALL NOT 仅以加权 `total` 择优——避免 `total` 被主体/卖相/画面完整度等高分维度主导而掩盖文案/LOGO 丢失。两版均未通过红线时仍择优交付（不丢弃产物）。

当择优选出的最终交付版**仍未通过红线**（`Pass=false`）时，系统 SHALL 下发 `review_failed` 信号并携带 `degraded:true` 与最终交付标记，如实反映「带瑕疵交付」，SHALL NOT 伪报 `review_passed`。该信号 MUST 加法式向后兼容：不识别新字段的旧客户端仍据 task_done 正常完成。产物照常持久化。

质量门控不可用时，系统 SHALL 优雅降级为及格，按原产物持久化，不阻塞适配产出。

#### Scenario: 合格产物直接通过
- **WHEN** 产物全部红线通过且加权总分 ≥ 阈值
- **THEN** 系统直接持久化该产物

#### Scenario: 必备要素缺失一票否决触发重生
- **WHEN** `key_elements_fidelity` 低于下限（核心主体丢失、LOGO 缺失、或要求保留的文字被改写/糊化）
- **THEN** 系统无视加权总分判不及格
- **AND** 把保真相关 hints（已按 placement 过滤）反馈给 gpt-image-2 并重生一次

#### Scenario: 高分不掩盖必备要素硬伤
- **WHEN** 加权总分越过阈值，但 `key_elements_fidelity` 低于下限
- **THEN** 系统仍判不及格，触发重生

#### Scenario: 无文案 placement 的 hints 不含文案补全建议
- **WHEN** placement 约定「无文案」，判官检测到定档大字/标签缺失
- **THEN** 判官生成的 hints SHALL NOT 含「补全定档大字」「补齐标签」等文案补充建议
- **AND** REVISE 段不出现与「无文案」约定矛盾的文字补全指令

#### Scenario: 无文案 placement 不因缺文案扣保真
- **WHEN** placement 约定「无文案」，产物保留核心主体与 LOGO 但无定档大字/标签
- **THEN** `key_elements_fidelity` SHALL NOT 因缺纯文案要素降低
- **AND** 核心主体/LOGO 齐全时该维度判定通过

#### Scenario: 重生产物复检并红线感知择优
- **WHEN** 首检不及格触发重生，重生产物产出
- **THEN** 系统对重生产物再打分一次（不触发第三轮生图）
- **AND** 按红线通过性 → `key_elements_fidelity` → `total` 的优先级择优持久化

#### Scenario: 两版均未过红线时优先保留 keyelem 更高的版本
- **WHEN** 首检版与重生版均未通过 `key_elements_fidelity` 红线（如首检 keyelem=30、重生 keyelem=25），且重生版 `total` 更高
- **THEN** 系统持久化首检版（keyelem 更高），SHALL NOT 因重生版 total 更高而交付 keyelem 更低的重生版

#### Scenario: 重生通过红线则取重生版
- **WHEN** 首检版未过红线、重生版通过红线
- **THEN** 系统持久化重生版（红线通过性优先于 total 比较）

#### Scenario: 带瑕疵交付下发降级信号
- **WHEN** 择优选出的最终交付版仍未通过红线（`Pass=false`）
- **THEN** 系统下发 `review_failed{degraded:true}` 而非 `review_passed`
- **AND** 仍持久化该版本并发布 task_done
- **AND** 不识别 degraded 字段的旧客户端仍正常完成任务

#### Scenario: 重生封顶为两轮生图
- **WHEN** 重生产物复检完成
- **THEN** 不触发第三轮生图；全程最多两轮生图、最多两次判官调用

#### Scenario: 判官不可用优雅降级
- **WHEN** 判官未配置 / 调用超时 / 结果解析失败
- **THEN** 系统视为及格，按原产物持久化，不向用户报错

#### Scenario: 裁剪快路径不触发质量门控
- **WHEN** 产物经确定性裁剪快路径产出
- **THEN** 不触发质量门控，直接持久化

## ADDED Requirements

### Requirement: 中等比例差的保文案重构约束

当 AI 重绘适配的**源图与目标宽高比差异超出裁剪快路径容差**（即不会走确定性裁剪、模型必须重构画幅）、**但目标未达极端档**（`≥3:1` 安全区构图约束不触发的中间区间，如 16:9 源 → 3:2 目标）时，系统 SHALL 在重绘提示中注入「保文案重构约束」，引导模型在重排画幅时保留并重新布局核心要素，而非自由重绘时丢弃文案/LOGO。

该约束 SHALL 要求：重构画幅时保留主体与 LOGO 完整可见、按新比例重新布局而非裁掉或省略。约束 SHALL 按 placement 文案约定过滤——placement 非「无文案」时追加「保留主标题/核心文案，可重排位置但不得丢弃或改写文字」；placement 含「无文案」时不提文案要素（与既有 `SizeNote` 过滤一致）。

源图与目标宽高比差异**在裁剪快路径容差以内**（如 900×500 对 16:9 源仅差约 1%）时，系统 SHALL NOT 注入该约束（走快路径或近乎无重构，维持既有行为）。目标为极端比例（`≥3:1`）时，仍由既有极端比例安全区构图约束处理，本约束 SHALL NOT 重复注入。

#### Scenario: 中等比例差注入保文案约束
- **WHEN** 源图 16:9、目标 900×600（3:2），比例差超出快路径容差但未达极端档
- **THEN** 重绘提示包含「重构画幅时保留主体/LOGO/文案完整可见、按新比例重排而非丢弃」类约束
- **AND** placement 非「无文案」时约束含主标题/核心文案的保留要求

#### Scenario: 近源比例不注入约束
- **WHEN** 目标 900×500（1.8）对 16:9 源仅差约 1%（快路径容差内）
- **THEN** 重绘提示不含保文案重构约束，维持既有行为

#### Scenario: 极端比例不重复注入
- **WHEN** 目标为极端比例（如 1120×280，4:1）
- **THEN** 仅由既有极端比例安全区构图约束处理，本约束不注入

#### Scenario: 无文案 placement 的重构约束不含文案
- **WHEN** 中等比例差触发约束，但 placement 约定「无文案」
- **THEN** 约束仅要求保留主体与 LOGO，不提主标题/核心文案
