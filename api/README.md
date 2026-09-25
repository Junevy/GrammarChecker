# GrammarChecker 前后端对接 API 手册

> 供前端联调使用。字段名与后端 `server/` 代码中 JSON tag 严格一致。
> 接口状态标注：**`[已实现]`** = 可直接对接（LLM 类接口已于 2026-09-25 接通 DeepSeek）。

---

## 0. 通用约定

- **Base URL**：`http://127.0.0.1:8899`（仅监听本机）。
- **数据格式**：除文件下载外，请求与响应均为 `Content-Type: application/json; charset=utf-8`。
- **请求体上限**：1MB。
- **时间字段**：ISO 8601 字符串（如 `2026-09-25T11:30:00+08:00`）。
- **错误响应**（非 2xx 时统一结构）：

```json
{
  "code": "api_key_missing",
  "message": "API KEY 未配置，请先在「配置」页填写"
}
```

| 状态码 | code | 场景 |
|---|---|---|
| 400 | `bad_request` | JSON 非法 / 必填字段为空 / 参数越界 |
| 400 | `api_key_missing` | LLM 类接口未配置 API KEY |
| 404 | `not_found` | 按 id 未找到记录 |
| 500 | `db_error` | 数据库错误 |
| 500 | `internal_error` | 服务器 panic 兜底（已 JSON 化） |
| 502 | `llm_upstream` | LLM 上游失败：网络不可达 / 鉴权被拒（4xx）/ 返回内容非合法 JSON |
| 503 | `timeout` | 请求处理超时（60s 上限，LLM 同步请求场景） |

---

## 1. 检查页

### 1.1 语法检查 `[已实现]`

`POST /api/check`

请求体：

```json
{
  "text": "Although she was tired, she have went to the store yesterday morning."
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| text | string | 是 | 待检查句子，≤500 字符（超限返回 400 bad_request；按字符计数，中文=1） |

响应 200（结构以前端 mock `app/vue/js/data.js` 的 `checkResult` 为准；LLM 供应商 DeepSeek，prompt 初稿 `server/llm/prompts.go`）：

```json
{
  "errTotal": 1,
  "translation": "虽然她很累，但她昨天早上去了商店。",
  "errors": [
    { "type": "主谓一致", "fix": "have went → went", "desc": "she 是第三人称单数……" }
  ],
  "example": { "en": "She went to the store yesterday.", "zh": "她昨天去了商店。" },
  "idiomatic": { "chip": "更地道", "tip": "……", "en": "……", "desc": "……" },
  "struct": {
    "comps": [ { "t": "Although she was tired", "r": "让步状语从句", "gray": false } ],
    "pattern": "从句 + 主句",
    "clause": { "chip": "让步状语从句", "lead": "Although…", "desc": "……" },
    "tense": { "chip": "一般过去时", "desc": "……" }
  }
}
```

说明：
- `errors` 为空数组即表示句子无错误（前端据此显示绿色对号与"未发现语法问题"卡片）；
- `errors[].type` 的短语同时作为趋势页 `type` 筛选（5.1）与「加入错词本」`error_type` 的取值口径；
- 结果同时写入检查历史（见 1.2）。

### 1.2 检查历史列表 `[已实现]`

`GET /api/history/check?limit=5`

| 查询参数 | 类型 | 说明 |
|---|---|---|
| limit | int | 缺省 20，上限 200；历史卡片取 5 |

响应 200：

```json
[
  {
    "id": 12,
    "sentence": "Although she was tired, she went to the store yesterday morning.",
    "error_count": 1,
    "result_json": "{…完整检查结果，结构同 1.1…}",
    "created_at": "2026-09-25T11:30:00+08:00"
  }
]
```

### 1.3 清空检查历史 `[已实现]`

`DELETE /api/history/check`

响应：`204 No Content`（无响应体）。

### 1.4 加入错词本 `[已实现]`

前端「加入错词本」倒计时结束后调用。`word` 必填，其余字段随检查结果携带、允许为空。

`POST /api/wrong-words`

请求体：

```json
{
  "word": "have went",
  "error_type": "主谓一致",
  "original_sentence": "Although she was tired, she have went to the store yesterday morning.",
  "corrected_sentence": "Although she was tired, she went to the store yesterday morning.",
  "analysis_json": "{…检查结果中的结构分析缓存，可空…}"
}
```

响应 201：

```json
{
  "id": 25,
  "word": "have went",
  "error_type": "主谓一致",
  "original_sentence": "…",
  "corrected_sentence": "…",
  "analysis_json": "",
  "created_at": "2026-09-25T11:30:00+08:00"
}
```

---

## 2. 仓库页

### 2.1 先前错误列表 `[已实现]`

`GET /api/wrong-words?q=&type=&from=&to=`

三个筛选条件为 **AND 组合**，均可省略：

| 查询参数 | 类型 | 说明 | 前端控件 |
|---|---|---|---|
| q | string | 按单词模糊搜索 | 搜索单词输入框 |
| type | string | 错误类型精确匹配 | 类型胶囊（如 `主谓一致`） |
| from / to | string | 日期范围 `yyyy-mm-dd` | 日期下拉（全部/最近7天/14天/30天，前端换算为日期） |

响应 200（数组）：

```json
[
  {
    "id": 25,
    "word": "childrens",
    "error_type": "名词复数",
    "original_sentence": "The childrens children is playing outside.",
    "corrected_sentence": "The children is playing outside.",
    "analysis_json": "{…}",
    "created_at": "2026-09-23T10:00:00+08:00"
  }
]
```

### 2.2 删除先前错误 `[已实现]`

`DELETE /api/wrong-words/{id}`

响应：`204`；id 不存在返回 `404`。

### 2.3 例句列表 `[已实现]`

`GET /api/sentences?q=`

| 查询参数 | 说明 |
|---|---|
| q | 按英文句子模糊搜索（搜索例句输入框） |

响应 200（数组，`source` 取值：`check` 来自检查流程 / `manual` 手动添加）：

```json
[
  {
    "id": 7,
    "english": "I received your letter yesterday.",
    "chinese": "我昨天收到了你的信。",
    "source": "check",
    "analysis_json": "",
    "created_at": "2026-09-24T09:00:00+08:00"
  }
]
```

### 2.4 手动添加例句 / 检查页收藏例句 `[已实现]`

`POST /api/sentences`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| english | string | 是 | 英文句子 |
| chinese | string | 否 | 中文翻译 |
| analysis_json | string | 否 | 结构分析缓存（JSON 字符串，来自 check 响应的 `struct` 段）；仓库例句卡片点击弹窗据此渲染结构分析 |
| source | string | 否 | `manual`（默认，手动添加）/ `check`（检查页「收藏例句」按钮） |

请求体：

```json
{
  "english": "Knowledge is power.",
  "chinese": "知识就是力量。",
  "analysis_json": "{\"comps\":[…],\"pattern\":…}",
  "source": "check"
}
```

响应：`201`，响应体同 2.3 单条结构；`source` 非法值返回 `400 bad_request`。

### 2.5 删除例句 `[已实现]`

`DELETE /api/sentences/{id}` → `204` / `404`。

### 2.6 辨析收藏列表（仓库 · 辨析选项卡） `[已实现]`

`GET /api/discriminations?q=&from=&to=&favorite=`

| 查询参数 | 说明 |
|---|---|
| q | 搜索单词 / 近义词（搜索收藏的解析输入框） |
| from / to | 日期范围 `yyyy-mm-dd` |
| favorite | 可选；`1`/`true` 仅收藏、`0`/`false` 仅未收藏；不传返回全部（非法值 400 bad_request） |

响应 200（数组）：

```json
[
  {
    "id": 3,
    "word": "receive",
    "synonyms_json": "[\"accept\"]",
    "result_json": "{…场景辨析完整结果，结构同 3.1…}",
    "favorite": false,
    "created_at": "2026-09-20T14:00:00+08:00"
  }
]
```

### 2.7 清空辨析历史 `[已实现]`

`DELETE /api/discrimination-history` → `204`。

### 2.8 删除单条辨析记录（仓库 · 辨析选项卡编辑模式） `[已实现]`

`DELETE /api/discriminations/{id}` → `204`。

| 场景 | 状态码 | code |
|---|---|---|
| 删除成功 | 204 | —（无响应体） |
| id 不存在 / 重复删除 | 404 | `not_found` |
| id 非正整数 | 400 | `bad_request` |

说明：仅删除该条记录，不影响收藏标记语义（删除即整条消失）。
前端入口：仓库页三个子选项卡共用的「编辑」按钮 → 卡片右上角「删除」角标（二次点击确认）。

---

## 3. 辨析页

### 3.1 单词辨析 `[已实现]`

`POST /api/discriminate`

请求体：

```json
{
  "word": "receive",
  "reuse_history": true
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| word | string | 是 | 待辨析单词 |
| reuse_history | bool | 否 | 与配置页「辨析复用历史记录」Toggle 一致；true 时后端先查本地命中直接返回 |

响应 200（契约以前端 mock `discResult` 为准；**LLM 新结果额外附 `id`**，供收藏接口使用）：

```json
{
  "id": 3,
  "sub": "receive vs accept",
  "core": "receive 表客观收到，accept 表主观接受",
  "words": [
    {
      "chip": "receive",
      "solid": true,
      "pos": "v. 收到",
      "scene": "客观动作",
      "en": "I received your letter yesterday.",
      "zh": "我昨天收到了你的信。"
    }
  ]
}
```

说明：
- 结果自动入库（discriminations 表），`synonyms_json` 由 `words` 中除主词外的 `chip` 聚合；
- **LLM 新结果附 `id`**；复用命中时**不带 `id`**（响应体为 LLM 结果结构附加 `reused: true` 与原记录 `created_at`）；
- `reuse_history=true` 且本地命中时**直接返回缓存结果，不要求已配置 API KEY**；
- 未命中或缓存为空时走正常 LLM 流程（复用短路先于 api_key 校验）。

### 3.2 辨析历史列表 / 清空

复用 2.6 / 2.7（辨析页历史记录卡）。

### 3.3 收藏 / 取消收藏 `[已实现]`

收藏为辨析记录上的标记位（discriminations 表 `favorite` 列），**写入幂等、取消不删除记录**；
列表用 2.6 的 `?favorite=1/0` 过滤。

- 收藏：`POST /api/discriminations/{id}/favorite` → `204`（重复调用结果一致）；
- 取消收藏：`DELETE /api/discriminations/{id}/favorite` → `204`（记录保留，仅清除标记）；
- id 不存在 → `404 not_found`。

前置条件：记录已存在于 DB——辨析/表达结果入库时响应附 `id`（3.1 / 4.1），直接使用即可。
表达页同款接口见 4.4。

---

## 4. 表达页

### 4.1 中文转英文表达 `[已实现]`

`POST /api/express`

请求体：

```json
{
  "text": "我昨天收到了你的信。"
}
```

响应 200（契约以前端 mock `expResult` 为准；**附 `id`** 供收藏接口使用）：

```json
{
  "id": 5,
  "core": "I received your letter yesterday.",
  "words": [
    { "chip": "口语", "solid": true, "pos": "日常对话", "scene": "…", "en": "…", "zh": "…" },
    { "chip": "书面", "solid": false, "pos": "正式文体", "scene": "…", "en": "…", "zh": "…" }
  ]
}
```

结果写入 expressions 表（表达历史）：`recommended` 取自 `core`；
`variants_json` 按 `{style, scene, sentence, translation}` 结构存储（由 `words` 条目转换）。

### 4.2 表达历史列表 `[已实现]`

`GET /api/expression-history?limit=20&favorite=`

| 查询参数 | 说明 |
|---|---|
| limit | 缺省 20，上限 200 |
| favorite | 可选；`1`/`true` 仅收藏、`0`/`false` 仅未收藏；不传返回全部（非法值 400 bad_request） |

响应 200（数组，时间倒序）：

```json
[
  {
    "id": 5,
    "chinese": "我昨天收到了你的信。",
    "recommended": "I received your letter yesterday.",
    "variants_json": "[{\"style\":\"口语\",\"scene\":\"…\",\"sentence\":\"…\",\"translation\":\"…\"}]",
    "favorite": false,
    "created_at": "2026-09-24T15:00:00+08:00"
  }
]
```

### 4.3 清空表达历史 `[已实现]`

`DELETE /api/expression-history` → `204`。

### 4.4 收藏 / 取消收藏 `[已实现]`

同 3.3 语义（expressions 表 `favorite` 列，幂等、取消不删记录）：

- 收藏：`POST /api/expression-history/{id}/favorite` → `204`；
- 取消收藏：`DELETE /api/expression-history/{id}/favorite` → `204`；
- id 不存在 → `404 not_found`。

列表用 4.2 的 `?favorite=1/0` 过滤。

---

## 5. 趋势页

### 5.1 趋势统计 `[已实现]`

`GET /api/trends?range=14&type=`

| 查询参数 | 类型 | 说明 |
|---|---|---|
| range | int | 天数，缺省 14，限 1-365 |
| type | string | 错误类型筛选（与 1.1 `errors[].type` 短语**精确匹配**）；口径：当日**不含**该类型错误的句子数 / 当日总数 × 100；不传退化为全量口径 |

响应 200：

```json
{
  "points": [
    { "date": "2026-09-25", "accuracy": 91.0 }
  ],
  "summary": {
    "today": 91.0,
    "avg": 78.6,
    "delta": 12.4
  }
}
```

说明：
- `summary.today` 当日无数据时为 `null`，此时 `delta` 恒为 0（无意义，前端忽略）；
- 口径（初始化版）：`accuracy = 当日 0 错误句子数 / 当日检查总数 × 100`；
  `avg` 为区间内**有检查记录日期**的平均（不按无记录日补 0）；`delta = 今日 − avg`；
- **无记录日期不产生数据点**（`points` 只含有检查记录的日子）；前端绘制时构建完整 14 天时间轴：
  空档日不画数据点、有值点之间直连；有效点不足 2 个时无法连成折线，需显示单点/空态提示；
  正确率映射建议 clamp（如 60%→底边、100%→顶边），低于下限贴底显示，避免坐标飞出画布。

---

## 6. 配置页

### 6.1 读取设置 `[已实现]`

`GET /api/settings`

响应 200（KV 对象；`api_key` 明文返回，掩码/显隐由前端负责）：

```json
{
  "api_key": "sk- local-api-key-2026",
  "reuse_discrimination_history": "1"
}
```

### 6.2 保存设置 `[已实现]`

`PUT /api/settings`

请求体（只传要改的键，白名单外的键整体拒绝返回 400）：

```json
{
  "api_key": "sk-xxxxxxxx",
  "reuse_discrimination_history": "0"
}
```

| 键 | 取值 | 说明 |
|---|---|---|
| api_key | string | LLM 密钥，仅存本地 SQLite |
| reuse_discrimination_history | `"0"` / `"1"` | 辨析复用历史开关，默认 `"1"` |

响应 200：保存后的完整设置（同 6.1）。

---

## 7. 成分说明与 LLM 用法示例（第三/四轮新增需求）

### 7.1 成分说明查询 `[已实现]`

检查页结构卡、仓库两个结构弹窗中点击成分胶囊时调用。
说明内容（是什么 / 有什么用 / 怎么用 / 示例）为**启动时预置 DB 的固定语法知识 seed**，
查询时只读 DB、不调用 LLM。已预置 23 个角色：
- 句子成分 11 个：主语 / 谓语 / 宾语 / 表语 / 定语 / 状语 / 补语 / 同位语 / 时间状语 / 地点状语 / 方式状语；
- 从句 12 类：主语从句 / 宾语从句 / 表语从句 / 同位语从句 / 定语从句 / 状语从句 / 让步状语从句 / 条件状语从句 / 原因状语从句 / 目的状语从句 / 结果状语从句 / 时间状语从句。

`GET /api/comp-info?role=主语`（role 需 URL 编码：`encodeURIComponent(role)`，**精确匹配**）

响应 200：

```json
{
  "role": "主语",
  "what": "句子动作的执行者或被描述的对象，通常由名词、代词或名词短语充当。",
  "why": "明确「谁」在做这件事，是句子陈述的核心对象；缺失主语的句子通常不完整。",
  "how": "一般置于谓语动词之前；可由名词、人称代词、动名词、不定式或主语从句充当。",
  "demo": "She writes letters every week. —— She 是主语，谓语 writes 与之保持第三人称单数。"
}
```

- 四个字段 `what / why / how / demo` 与前端 `compInfo` mock 的键一致，可直接替换；
- 未收录的角色返回 `404 not_found`，前端回退本地「默认」说明（data.js 的 `compInfo['默认']`）；
- **前端注意**：role 为精确匹配。前端成分的 `r` 值可能带修饰（如 `名词短语 · 宾语`），
  联调时须先归一化为基础角色名（`名词短语 · 宾语` → `宾语`）再请求，否则会 404 走默认说明；
- role 传空或缺省 → `400 bad_request`。

### 7.2 生成用法示例（LLM） `[已实现]`

成分说明弹窗内「生成用法示例」按钮调用，LLM 同步生成，前端需展示 loading（当前 mock 900ms）。

`POST /api/llm/usage-example`

请求体：

```json
{
  "role": "主语",
  "sentence": "Although she was tired, she went to the store yesterday morning."
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| role | string | 否 | 成分角色 |
| sentence | string | 是 | 当前句子，供 LLM 生成贴合语境的示例（为空返回 400 bad_request） |

响应 200：

```json
{
  "example": "The eager students finished their homework before dinner."
}
```

说明：结果为一次性生成，不入库。

错误：`400 api_key_missing`（未配置 KEY）→ 前端引导跳转配置页；`502 llm_upstream`（上游失败/返回非法）。

---

## 8. 健康检查（非业务接口）

`GET /api/ping` → `{"ok": true}`。供托盘/脚本探活。

---

## 附：联调替换对照表（app/vue/js/data.js → API）

| 前端 mock 段 | 对应接口 | 页面 |
|---|---|---|
| wrongWords / typeList | GET /api/wrong-words（typeList 由前端聚合 error_type 去重计数） | 仓库·先前错误 |
| sentences | GET /api/sentences | 仓库·例句 |
| discPairs | GET /api/discriminations | 仓库·辨析 |
| checkResult / compInfo | POST /api/check / GET /api/comp-info?role=xxx | 检查页 |
| checkHistory / discHistory / expHistory | GET /api/history/check / discriminations / expression-history | 三个历史卡片 |
| discResult | POST /api/discriminate | 辨析页 |
| expResult | POST /api/express | 表达页 |
| 结果卡收藏按钮（favDisc / favExp） | POST·DELETE /api/discriminations/{id}/favorite、/api/expression-history/{id}/favorite（见 3.3 / 4.4） | 辨析页 / 表达页 |
| 检查页「收藏例句」按钮（favSent） | POST /api/sentences（english=按 fix 修正后的句子，chinese=translation，analysis_json=struct，source=check，见 2.4）、取消收藏 DELETE /api/sentences/{id}（见 2.5） | 检查页 |
| trend | GET /api/trends | 趋势页 |
| 设置（reuseOn / keyVisible 初始值） | GET·PUT /api/settings | 配置页 |
