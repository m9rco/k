## Context
所有模型后端目前共用 yunwu.ai 一个供应商,凭证集中在 `YUNWU_API_KEY` / `YUNWU_BASE_URL`。配置装配(`config.Load`)已用 `envFirst(def, keys...)` 给 chat/image/video 做了"专属变量 → 公共默认"的两层回退,但:实现散落、命名不统一(video=`HAPPYHORSE_*`)、crawl 未接入、公共名 `YUNWU_*` 与"换供应商"目标语义相悖。

约束(来自 `project.md`):仅小团队内部使用,凭证可硬编码进环境变量,不做加密/鉴权;模型用户不可选;改动应"简单优先"。

## Goals / Non-Goals
- Goals
  - 每个模型后端可独立覆盖 `provider / base_url / api_key`,任一缺省回退公共值,再回退内置默认。
  - 公共回退命名语义中立(`COMMON_*`),与具体供应商解耦。
  - 全部后端(含 crawl)走同一套解析逻辑,行为可预测、可单测。
  - 完全向后兼容现有 `YUNWU_*` / `HAPPYHORSE_*` / `CRAWL_ENDPOINT` 部署。
- Non-Goals
  - 不纳入 COS 凭证(多字段、非模型 API)。
  - 不改协议实现、不改运行时选模逻辑。
  - 不引入配置文件格式或动态热加载。

## Decisions

### D1: 三层回退优先级(每个字段独立解析)
对每个后端的每个字段,按以下优先级取第一个非空值:

```
provider:  <PREFIX>_PROVIDER  →  COMMON_PROVIDER  →  内置默认(如 "openai")
base_url:   <PREFIX>_BASE_URL  →  COMMON_BASE_URL  →  YUNWU_BASE_URL(别名) →  内置默认
api_key:    <PREFIX>_API_KEY   →  COMMON_API_KEY   →  YUNWU_API_KEY(别名)  →  ""
model:      <PREFIX>_MODEL     →  内置默认(模型 id 无公共回退)
```

字段**逐项**回退而非整组回退:可以只覆盖某模型的 `BASE_URL` 而继续共用公共 `API_KEY`。这正是"换供应商时只补差异项"的关键。

### D2: 统一解析辅助 `resolveEndpoint`
集中三层回退,替换散落的 `envFirst`:

```go
// commonDefaults 在 Load 开头解析一次,承载 COMMON_* 及其 YUNWU_* 兼容别名。
type commonDefaults struct{ provider, baseURL, apiKey string }

func loadCommon() commonDefaults {
    return commonDefaults{
        provider: env("COMMON_PROVIDER", ""),
        baseURL:  envFirst("https://yunwu.ai/v1", "COMMON_BASE_URL", "YUNWU_BASE_URL"),
        apiKey:   envFirst("", "COMMON_API_KEY", "YUNWU_API_KEY"),
    }
}

// resolveEndpoint 解析一个后端的 provider/baseURL/apiKey/model 四元组。
// defProvider/defModel 为该后端的内置默认;model 不参与公共回退。
func (c commonDefaults) resolveEndpoint(prefix, defProvider, defModel string) endpoint {
    return endpoint{
        provider: envFirst(orDefault(c.provider, defProvider), prefix+"_PROVIDER"),
        baseURL:  envFirst(c.baseURL, prefix+"_BASE_URL"),
        apiKey:   envFirst(c.apiKey, prefix+"_API_KEY"),
        model:    env(prefix+"_MODEL", defModel),
    }
}
```

`ModelConfig` / `ImageProviderConfig` 由该四元组填充。`ImageProviderConfig` 新增 `Provider` 字段与 `ModelConfig` 对齐(image/video 当前实现不分支 provider,字段先就位,为后续换非 openai 兼容供应商留口)。

### D3: 兼容别名清单(优先级低于 COMMON_*、高于内置默认)
| 后端 | 统一 prefix | 兼容别名 |
|------|-------------|----------|
| 公共 | `COMMON_*` | `YUNWU_BASE_URL` / `YUNWU_API_KEY` |
| chat 主 | `CHAT_PRIMARY_*` | —— |
| chat 测 | `CHAT_TEST_*` | `DEEPSEEK_API_KEY`(api_key) |
| image 主 | `IMAGE_PRIMARY_*` | —— |
| image 备 | `IMAGE_BACKUP_*` | —— |
| video | `VIDEO_*` | `HAPPYHORSE_BASE_URL` / `HAPPYHORSE_API_KEY` / `HAPPYHORSE_MODEL` |
| crawl | `CRAWL_*` | `CRAWL_ENDPOINT`(base_url) |

别名解析次序:`<PREFIX>_X → COMMON_X → <ALIAS> → 默认`。即专属与公共仍优先于旧别名,旧别名仅作为公共/默认之间的兜底,保证既有部署不回归。

### D4: crawl 的特殊处理
crawl 的 `base_url`(端点)**无公共默认**:模型 API 的公共 URL(`/v1`)对 crawl 无意义,缺省即视为"未配置",能力优雅降级(现有 `Configured()` 语义不变)。仅 `api_key` 回退 `COMMON_API_KEY`。`CRAWL_PROVIDER` 字段先预留、暂不被 `httpSource` 消费。

## Risks / Trade-offs
- 风险:别名次序若写错会让既有部署回归(如 `YUNWU_*` 被误置于专属之上)。→ 用 D3 表 + 专门单测固化每条优先级链。
- 风险:`ImageProviderConfig.Provider` 增字段但暂无人消费,可能被误读为"已支持多协议生图"。→ 字段注释标注"reserved";spec 标明不改协议实现。
- 取舍:crawl 的 base_url 不给公共默认,与模型后端不完全对称。→ 在 spec 显式写明理由,避免后人"补齐对称"反而把未配置变成误配置。

## Migration Plan
1. 加 `resolveEndpoint` / `loadCommon`,不删旧逻辑。
2. 逐后端切到新解析,跑现有 `config_test.go` 确认默认值不变。
3. 补三层回退 + 别名单测。
4. 更新 `.env.example`(`COMMON_*` 为主、旧名标注"alias")。
回滚:本变更纯配置装配层,无数据/接口变更,`git revert` 即可。

## Open Questions
- 无(COMMON_* 命名、覆盖范围含 crawl、维度含 provider 均已与用户确认)。
