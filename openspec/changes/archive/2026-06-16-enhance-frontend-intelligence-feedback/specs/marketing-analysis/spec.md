## MODIFIED Requirements

### Requirement: 分析报告流式 chat 输出
系统 SHALL 以**流式增量**方式把分析报告输出到 web 对话区。分析完成后，报告块 SHALL **自动折叠**（collapsed = true），使对话区保持简洁；用户可手动展开查看完整报告。折叠标题 SHALL 显示「宣发分析」，展开状态 SHALL 显示「分析中」。

#### Scenario: 报告流式显示
- **WHEN** 视觉分析进行中
- **THEN** 分析报告逐段实时出现在 web 对话区
- **AND** 用户无需等待分析全部完成才看到输出

#### Scenario: 分析完成自动折叠
- **WHEN** 分析报告流式输出完毕（done = true）
- **THEN** 分析块立即收起（collapsed = true），仅显示「宣发分析」摘要行
- **AND** 用户点击该行可手动展开查看完整报告内容

#### Scenario: 分析阶段在适配前可见
- **WHEN** 完整适配流程执行
- **THEN** 对话区呈现：上传阶段 → 分析流式报告（完成后折叠）→ 适配开始
- **AND** 各阶段有清晰的阶段标识或分隔
