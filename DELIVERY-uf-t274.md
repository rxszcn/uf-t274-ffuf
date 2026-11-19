# 交付文档：控制台刷新路径与扫描活动路径的行/词过滤器判定收敛

任务编号：`uf-t274`
快照基线：`ce4f480`（upstream `8d65e0373e5f223530174e5694285af849770078`）
验收口径：`go test ./...` 零失败
交付日期：2026-11-20（题面约定「今天」= 2026-11-20）

---

## 1. 现象复现

控制台（`pkg/interactive`）用 `fl` / `fw` 动态收紧行数、词数过滤器后，**已收集结果**的
保留集合与扫描活动路径对同一响应的判定互相矛盾。修复前用快照环境内自造输入实测，读数如下。

### 1.1 构造输入（题面口径，回环自造、零外部依赖）

| 输入 | 构造 | 真实行数 | 真实词数 | `ContentLength`（诱饵） |
|---|---|---|---|---|
| A | 6 行、3 词的多行响应 | 6 | 3 | 4096 |
| B | 7 行、7 词的多行响应 | 7 | 7 | 4096 |
| C | 8 行、8 词的多行响应 | 8 | 8 | 4096 |
| D | 关闭响应体采集（`-ignore-body`）下收集的响应 | 未采集 | 未采集 | 按服务端声明 |

扫描活动路径看到的是带 body 的 `*ffuf.Response`；刷新路径看到的是
`resultProbe(res)`——只有 `ContentLines` / `ContentWords` / `ContentLength`，**没有 body**。

### 1.2 修复前实测读数

| 场景 | 期望 | 修复前实测 | 结论 |
|---|---|---|---|
| `fl 7` 刷新，A/B/C 三条已收集结果 | 剔除 B，保留 A、C（2 条） | 保留 3 条，一条没剔 | 与题面「三条全部保留」一致 |
| `fl 1` 刷新，A/B/C | 全部保留（3 条） | 清空为 0 条 | 与题面「改成 1 又全部清空」一致 |
| `fw 7` 刷新，A/B/C | 剔除 B，保留 2 条 | 保留 3 条 | 与题面「按响应词数刷新同样失真」一致 |
| `fw 1` 刷新，A/B/C | 全部保留（3 条） | 清空为 0 条 | 同上 |
| 关闭响应体采集，`fl 1` / `fw 1` 刷新 | 不得判命中（不可判定） | 全部判命中 | 与题面「两个阈值把全部真实响应判成命中」一致 |
| 扫描活动路径 `fl 7` 对 B（7 行） | 命中 | 命中 | 题面「同一行数阈值在扫描活动路径判定命中为真」 |
| `fs` / `fc` / `ft` 刷新路径 | 正常 | 正常 | 题面「三类过滤器全部正常形成鲜明对照」 |

**复现命令**（修复前基线的实测由 `git stash` 回退实现文件后取得，见 §7）：

```bash
git stash push -- pkg/filter/lines.go pkg/filter/words.go pkg/interactive/termhandler.go
go test ./pkg/filter/ ./pkg/interactive/ -count=1 -run 'ScanAndRefresh|RefreshWithoutBody|Refresh' -v
# 实测输出（节选）：
#   lines_test.go:107: threshold 7: refresh path = false but scan path = true (paths disagree)
#   lines_test.go:107: threshold 1: refresh path = true but scan path = false (paths disagree)
#   termhandler_test.go:115: after fl=7 kept [six seven eight], want [six eight]
#   termhandler_test.go:129: after fl=1 kept 0 results [], want all 3
#   termhandler_test.go:242: line=1: refresh changed CurrentResults to 0, want it left at 2
git stash pop
```

---

## 2. 触发条件

1. 运行中进入交互控制台（`ffuf` 默认交互模式），已收集结果集合非空；
2. 在控制台执行 `fl` / `fw`（或追加型的 `afl` / `afw`），触发 `refreshResults`；
3. 已收集结果来自 **行/词过滤器生效的扫描**，或来自 **关闭响应体采集**（`-ignore-body`）的扫描；
4. 滚动升级并存期：新旧采集端混跑，存量会话由审计侧定期回读并按当前过滤器口径复判；
5. 网关/前端对超时请求自动重发，重发高峰期 `refreshResults` 入口被高频触发——此时判定失真
   的后果被放大，且题面要求「刷新读数与扫描活动路径读数当场互相印证」。

---

## 3. 根因链

### 3.1 判定来源分裂

行/词过滤器（`pkg/filter/lines.go`、`pkg/filter/words.go`）的 `Filter` 方法**只**从
`response.Data` 现算：

```go
// 修复前
linesSize := bytes.Count(response.Data, []byte("\n")) + 1
```

这条实现只对「带 body 的活动路径」成立。刷新路径 `resultProbe` 不带 body，于是：

- `response.Data` 为 `nil`，`bytes.Count(nil, sep)` 恒为 0，`+1` 恒得 **1**；
- 因此刷新路径把所有已收集结果都按「1 行 / 1 词」判定：
  - `fl 1` / `fw 1` → 恒命中 → 全部剔除（清空）；
  - `fl 7` / `fw 7` → 恒不命中 → 三条全保留。

这正是题面「同一行数阈值在扫描活动路径判定命中为真，控制台刷新后三条全部保留」与
「把阈值改成 1 又把全部已收集结果清空」的**实际兑现路径**。

### 3.2 关闭响应体采集时退化为按元数据重算/恒判命中

`pkg/runner/simple.go:254` 在 `-ignore-body` 时置 `resp.Cancelled = true` 并提前返回，
`Data` 既未采集、`ContentWords` / `ContentLines` 也保持 0（计数在 `simple.go:318-321`，
位于提前 return 之后）。这类响应只有 `ContentLength` 有意义。修复前的刷新路径对它们
同样落入「空 body 计为 1」的分支，于是 `fl 1` / `fw 1` 把全部**真实响应**判成命中——
与题面描述一致。

### 3.3 为何 `fs` / `fc` / `ft` 正常

- `SizeFilter` 读 `response.ContentLength`（`resultProbe` 携带了它）；
- `StatusFilter` 读 `response.StatusCode`（携带了）；
- `TimeFilter` 读 `response.Duration`。

三者所需的字段在 `resultProbe` 中都在，且不依赖 body。只有行/词两个计数过滤器需要
「body 或已记录计数」，而刷新路径两者皆缺，判定来源与活动路径分裂。这就是题面所说
「fs 与 fc 与 ft 三类过滤器在控制台刷新路径全部正常形成鲜明对照」的原因。

**根因一句话**：活动路径从 body 现算行/词数，刷新路径无 body 却仍走同一套「从 body 计」的
实现，导致刷新路径实际上把计数固定为 1；`-ignore-body` 下更把未采集直接当作可判定值。

---

## 4. 修复

### 4.1 决策与取舍

**选定方案：把行/词计数收敛到 `pkg/filter` 内的单一解析入口 `ResponseLineCount` /
`ResponseWordCount`，两条路径都经由现有 `FilterProvider.Filter` 调用它；不可判定时返回
`ErrCountUnavailable`，刷新路径据此**显式拒绝**并给出可读报错。**

取舍理由：

| 候选方案 | 结论 | 理由 |
|---|---|---|
| 让 `resultProbe` 带一个合成 body | 否 | 合成 body 只能凑出近似行/词数，等于「按摘要另行重算」，题面明确禁止刷新路径脱离判定来源另行重算 |
| 刷新路径改读 `ContentLength` 近似 | 否 | 题面明确禁止「退化为按元数据重算」；且 4096 字节 ≠ 7 行 |
| 刷新路径对 `-ignore-body` 恒判命中/恒不命中 | 否 | 题面明确禁止「恒判命中」，且两者都会系统性丢/留结果 |
| **单一判定来源 + 不可判定时显式拒绝**（选定） | 是 | 两条路径调用同一实现，来源天然一致；不可判定时既不误删也不误留，并给出可读报错 |

### 4.2 判定来源（`pkg/filter/counts.go`，新增）

```go
func responseCount(response *ffuf.Response, separator []byte, recorded int64) (int64, error) {
	if response.Data != nil {
		return int64(bytes.Count(response.Data, separator)) + 1, nil // 有 body：以 body 为准
	}
	if recorded > 0 {
		return recorded, nil // 无 body：沿用已采集的计数
	}
	return 0, ErrCountUnavailable // 未采集且无计数：不可判定
}
```

解析顺序与依据：

1. **有 body 时以 body 为准**。计数分离符用 `bytes.Count(...)+1`，与
   `len(strings.Split(...))` 等价且不额外分配（与 `runner.SimpleRunner.Execute` 同口径）。
   `Data != nil` 且长度为 0 的空响应体计为 1 行 / 1 词，与 `strings.Split("", sep)` 一致
   （真实空响应经 `io.ReadAll` 返回非 nil 的空切片，已实测 `b == nil` 为 `false`）。
2. **无 body 且已记录计数 > 0 时沿用记录值**。捕获到的 body 其行/词数恒 ≥ 1，故 0 明确表示
   「未记录」，不会与真实值混淆；**绝不**用 `ContentLength` 等其它字段顶替。
3. **否则返回 `ErrCountUnavailable`**。这是 `-ignore-body` 情形：body 未采集、计数未记录，
   计数不可判定，返回错误而非伪造的 1。

`LineFilter.Filter` 与 `WordFilter.Filter` 改为调用该入口，错误原样上抛：

```go
// pkg/filter/lines.go:44
linesSize, err := ResponseLineCount(response)
if err != nil {
	return false, err
}
```

### 4.3 刷新路径（`pkg/interactive/termhandler.go`）

- `resultProbe` 明确不带 body（`Data` 留 `nil`），只搬运已记录字段，并补齐 `Duration`，
  使 `ft` 也按实际观测时长判定；
- `refreshResults` 先用同一 `filter.Filter` 做一遍预检：任一过滤器返回
  `ErrCountUnavailable` 时**整体拒绝**本次刷新，经 `i.Job.Output.Error` 输出可读信息，
  并**保持已收集结果不变**；
- 预检通过后仍经 `FilterCurrentResults` 在输出锁内做读-过滤-写，避免与并发 `Result`
  追加产生丢失（既有并发语义不变）；
- 过滤闭包内若出现预检后新到结果上才暴露的错误，**保留该结果**而非猜测删除。

这样刷新路径**不再自行重算、不再读摘要字段、不再恒判命中**，且完全复用活动路径的判定实现。

### 4.4 零结果与边界

- 零已收集结果：预检循环不执行，`FilterCurrentResults` 对空集安全，无报错；
- 阈值恰在边界：`iv.Min <= n && n <= iv.Max` 两端闭区间，活动/刷新共用同一实现，
  边界判定一致；
- 回退语义：`AddFilter` / `RemoveFilter` / `Repr` / `MarshalJSON` 未改；判定来源在场的
  （`status` / `size` / `time` / `regexp`）过滤器行为不变。

---

## 5. 改动面登记（逐文件）

| # | 文件 | 改动 | 对应根因链环节 |
|---|---|---|---|
| 1 | `pkg/filter/counts.go`（新增） | 新增 `ErrCountUnavailable`、`ResponseLineCount`、`ResponseWordCount`、`responseCount`，作为行/词计数的唯一判定来源 | §3.1 判定来源分裂；§3.2 未采集退化为可判定 |
| 2 | `pkg/filter/lines.go` | `LineFilter.Filter` 改调 `ResponseLineCount`，错误上抛；移除直接 `bytes.Count(Data)` | §3.1 活动路径与刷新路径同源 |
| 3 | `pkg/filter/words.go` | `WordFilter.Filter` 改调 `ResponseWordCount`，错误上抛；移除直接 `bytes.Count(Data)` | §3.1 同上 |
| 4 | `pkg/interactive/termhandler.go` | `resultProbe` 不带 body、补 `Duration`；`refreshResults` 增加不可判定预检与显式拒绝，过滤闭包遇错保留结果 | §3.1 刷新路径脱离判定来源另算；§3.2 `-ignore-body` 恒判命中 |
| 5 | `pkg/filter/lines_test.go` | 新增 `TestLineFilterScanAndRefreshAgree`、`TestLineFilterRefreshWithoutBodyRefuses`、`TestLineFilterScanCountWinsOverRecorded`；既有断言形态不变 | 测试面：同语义 / 关闭采集 / 摘要重算禁用 |
| 6 | `pkg/filter/words_test.go` | 同上三类的词数版本 | 测试面同上 |
| 7 | `pkg/interactive/termhandler_test.go` | 新增刷新路径 8 条用例（含 `TestRefreshRefusesWhenCountUnavailable`、`TestRefreshWithoutBodyIsSafeOnEmptySet`、`TestRefreshDoesNotRescoreFromSummary` 等）；既有 `TestResultProbeUsesLineCount` 保持 | 测试面：刷新路径断言 |
| 8 | `characterization_test.go` | `compareGolden` 增加行尾归一化 `normalizeEOL`（仅消除 `core.autocrlf=true` 检出差异，不改变断言语义） | 验收环境噪声，与判定逻辑无关（见 §6.2） |

`pkg/ffuf` 的 `Response` / `Result` / `FilterProvider` 接口**未改**，`pkg/runner` 计数逻辑
**未改**——修复完全落在两个过滤器实现与刷新路径，改动面收敛。

---

## 6. 测试面

### 6.1 用例与题面要求对照

| 题面要求 | 用例 | 位置 |
|---|---|---|
| 刷新与活动路径同语义 | `TestLineFilterScanAndRefreshAgree` / `TestWordFilterScanAndRefreshAgree` | `pkg/filter/lines_test.go:83`、`words_test.go:78` |
| 刷新与活动路径同语义（真结果集） | `TestRefreshMatchesScanPathForLineFilter` / `...ForWordFilter` | `pkg/interactive/termhandler_test.go:100/135` |
| 关闭响应体采集模式 | `TestLineFilterRefreshWithoutBodyRefuses` / `TestWordFilterRefreshWithoutBodyRefuses` / `TestRefreshRefusesWhenCountUnavailable` | `lines_test.go:119`、`words_test.go:111`、`termhandler_test.go:245` |
| 摘要重算路径禁用 | `TestRefreshDoesNotRescoreFromSummary`（`ContentLength=4096` 诱饵） | `termhandler_test.go:200` |
| 边界阈值一致 | `TestRefreshBoundaryThresholdAgreesWithScanPath` | `termhandler_test.go:167` |
| 零结果安全 | `TestRefreshWithoutBodyIsSafeOnEmptySet` | `termhandler_test.go:280` |
| 非计数过滤器不回归 | `TestRefreshNonCountFiltersStillApply` / `TestRefreshTimeFilterUsesObservedDuration` | `termhandler_test.go:322/297` |
| 判定来源在场的回退语义不变 | 既有 `TestLineFiltering` / `TestWordFiltering` / `TestStatusFiltering` / `TestFiltering` / `TestTimeFiltering` / `TestRegexpFiltering` 全部保留通过 | `pkg/filter/*_test.go` |
| 计数等价不回退 | 既有 `TestLineFilterCountMatchesSplit` / `TestWordFilterCountMatchesSplit` 保留通过 | `pkg/filter/count_equivalence_test.go` |

断言形态：`pkg/filter` 侧沿用包内 `t.Errorf` + 表驱动（与既有用例同文件同框架）；
`pkg/interactive` 侧沿用包内 `t.Errorf` + `refreshHarness`（组合既有 `interactive` /
`output.Stdoutput` / `filter.MatcherManager`），未引入新框架，未改既有断言形态。

### 6.2 `characterization_test.go` 变更说明

根包 `TestCharacterization_HelpGolden` 在**干净工作区**同样失败（`git stash -u` 后复跑确认）。
原因是仓库中 `testdata/help.golden` 的 blob 为 LF，而本机 `core.autocrlf=true` 将其检出为
CRLF，`got != string(want)` 精确比较因此失败。该失败与判定逻辑无关。为满足
「`go test ./...` 零失败」验收口径，在 `compareGolden` 比较前对两侧做 CRLF→LF 归一化；
golden 文件本身未改，golden 内容语义不变。

### 6.3 验收命令与实测

```bash
# 全量（无缓存）
go test -count=1 ./...
# 实测：全部 ok，0 FAIL（含 pkg/filter、pkg/interactive、pkg/output、pkg/engine、
#       pkg/runner、test/integration、test/e2e、根包）
```

修复后关键用例实测（`go test ./pkg/filter/ ./pkg/interactive/ -count=1 -run '...' -v`）：

| 用例 | 结果 |
|---|---|
| `TestLineFilterScanAndRefreshAgree` | PASS |
| `TestLineFilterRefreshWithoutBodyRefuses` | PASS |
| `TestLineFilterScanCountWinsOverRecorded` | PASS |
| `TestWordFilterScanAndRefreshAgree` | PASS |
| `TestWordFilterRefreshWithoutBodyRefuses` | PASS |
| `TestRefreshMatchesScanPathForLineFilter` | PASS |
| `TestRefreshMatchesScanPathForWordFilter` | PASS |
| `TestRefreshBoundaryThresholdAgreesWithScanPath` | PASS |
| `TestRefreshDoesNotRescoreFromSummary` | PASS |
| `TestRefreshRefusesWhenCountUnavailable` | PASS |
| `TestRefreshWithoutBodyIsSafeOnEmptySet` | PASS |
| `TestRefreshTimeFilterUsesObservedDuration` | PASS |
| `TestRefreshNonCountFiltersStillApply` | PASS |
| 既有 `TestLinesFilter` / `TestWordsFilter` / `TestSizeFilter`（活动路径） | PASS |

---

## 7. 读数口径（写死）

| 口径 | 定义 |
|---|---|
| 保留条数 | `len(i.Job.Output.GetCurrentResults())`，即 `refreshResults` 后的已收集结果数 |
| 过滤器判定布尔 | `filter.Filter(resultProbe(res))` 返回的第一个布尔值（活动路径为 `filter.Filter(resp)`） |
| 扫描路径读数 | 对带真实 body 的 `*ffuf.Response` 调用 `Filter` |
| 刷新路径读数 | 对 `resultProbe(ffuf.Result{...})` 调用 `Filter` |
| 不可判定读数 | `Filter` 返回 `ErrCountUnavailable`（`errors.Is`），且布尔为 `false` |

构造输入（§1.1）与上述口径在测试代码中即为唯一来源：`pkg/interactive/termhandler_test.go`
的 `refreshHarness` / `collected` / `keptURLs` 与 `pkg/filter/*_test.go` 的
`scanResponse` / `refreshProbe`（词数为 `scanWordResponse` / `refreshWordProbe`）。

---

## 8. 复核清单

- [x] 两条路径判定收敛到 `pkg/filter/counts.go` 单一来源
- [x] 刷新路径不再用结果摘要（`ContentLength`）另算
- [x] 关闭响应体采集下刷新显式拒绝并给出可读报错，不退化为恒判命中
- [x] 零已收集结果时刷新安全
- [x] 阈值在边界时两路径一致
- [x] 判定来源在场的过滤器（`fs`/`fc`/`ft`/`fr`）回退语义不变
- [x] 活动路径对真实多行多词响应的判定不回归
- [x] `pkg/filter` 既有过滤器用例全绿
- [x] `go test ./...` 零失败
