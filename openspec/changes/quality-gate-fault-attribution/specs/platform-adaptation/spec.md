# platform-adaptation (delta)

## MODIFIED Requirements

### Requirement: 适配后质量打分门控与单次重生
系统 SHALL 在质量门控判官的 JSON 输出中包含 `fault_source` 字段（`"repaint"` | `"outpaint"` | `"both"`），用于标识缺陷来源：`repaint` 指 gpt-image-2 重绘步骤有问题，`outpaint` 指 Gemini outpaint 填充步骤有问题，`both` 指两步均有问题。判官 MUST 仅在产物确实经过了 outpaint 步骤时使用 `outpaint`，否则默认 `repaint`。

系统 SHALL 在 outpaint 步骤执行前快照 gpt-image-2 产物（`preOutpaintData`）。质量门控判定不及格时，系统 SHALL 依 `fault_source` 选择重试策略：

- `fault_source == "outpaint"` 且 `preOutpaintData` 非空：系统 SHALL 跳过 gpt-image-2 调用，直接以 `preOutpaintData` 重新执行 outpaint + 收敛步骤；hints SHALL 注入 outpaint prompt 而非重绘 prompt。
- 其他情况（`repaint`、`both`、`preOutpaintData` 为空、`fault_source` 缺失/解析失败）：系统 SHALL 整条流水线重跑（行为与 fault_source 引入前一致）。

重生封顶一次的约束 SHALL NOT 改变：`Attempt=1` 的产物 SHALL NOT 再次进入质量门控。

#### Scenario: outpaint 缺陷精确回退跳过重绘
- **GIVEN** 一张经过 outpaint 步骤的适配产物，判官判定 `fault_source="outpaint"`
- **WHEN** 质量门控触发重试
- **THEN** 系统跳过 gpt-image-2 重绘调用，以快照的 preOutpaintData 重跑 outpaint + 收敛
- **AND** hints 指向 outpaint 改进

#### Scenario: 重绘缺陷整条重跑
- **GIVEN** 一张适配产物，判官判定 `fault_source="repaint"` 或 `"both"`
- **WHEN** 质量门控触发重试
- **THEN** 系统整条流水线重跑，hints 注入重绘 prompt
- **AND** 行为与 fault_source 引入前一致

#### Scenario: 未触发 outpaint 的产物不走 outpaint-only 重试
- **GIVEN** 一张 ratio 差 ≤ 0.18、无需 outpaint 的适配产物
- **WHEN** 判官返回任意 fault_source（包括 "outpaint"）
- **THEN** 系统忽略 outpaint 分类，整条流水线重跑（preOutpaintData 为空）
