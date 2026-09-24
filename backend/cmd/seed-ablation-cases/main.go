// Command seed-ablation-cases populates the WP3 Context & Memory ablation
// benchmark dataset into the WABench tables.
//
// Usage:
//
//	go run ./cmd/seed-ablation-cases/                  # seed suite, fixtures, cases
//	go run ./cmd/seed-ablation-cases/ --seed-candidates # seed ablation candidates A-D
//	go run ./cmd/seed-ablation-cases/ --seed-runs       # create ablation runs (requires candidates)
//	go run ./cmd/seed-ablation-cases/ --seed-runs-v2    # create v2 ablation runs for the 200+ case rerun
//	go run ./cmd/seed-ablation-cases/ --seed-memories   # seed Tier1 hard preferences for the memory user (C/D candidates)
//	go run ./cmd/seed-ablation-cases/ --wipe-memories   # delete all memories of the memory user (user_memories / memory_entities / dismissals)
//	go run ./cmd/seed-ablation-cases/ --start-runs      # candidates + runs + print execution commands
//	go run ./cmd/seed-ablation-cases/ --status           # show ablation run progress
//
// The script is idempotent: it uses ON CONFLICT to skip already-seeded rows.
// It reads DATABASE_URL (or TEST_DATABASE_URL) from the environment.
// Memory seeding additionally honors the runner LLM contract
// (LLM_API_KEY / LLM_BASE_URL / LLM_MODEL) and DASHSCOPE_* for embeddings;
// the Create path itself needs neither (no LLM/embedding call is required to
// persist a Tier1 hard preference).
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	memsvc "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ── configuration ────────────────────────────────────────────────────────────

const (
	suiteID      = "ablation-benchmark-v1"
	suiteVersion = "v1.0.0"
	partition    = "development"
	visibility   = "public"
	privacyLevel = "public"
	styleRef     = "luminbuddy.builtin-style.default"
)

var rubricWeights = map[string]int{
	"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15,
	"styleConsistency": 15, "directUsability": 20,
}

// ── CLI flags ────────────────────────────────────────────────────────────────

var (
	flagSeedCandidates = flag.Bool("seed-candidates", false, "seed ablation candidates A-D")
	flagSeedRuns       = flag.Bool("seed-runs", false, "create ablation runs (requires candidates)")
	flagSeedRunsV2     = flag.Bool("seed-runs-v2", false, "create v2 ablation runs for the 200+ case rerun (requires candidates + cases)")
	flagSeedMemories   = flag.Bool("seed-memories", false, "seed Tier1 hard preferences for the ablation memory user (C/D candidates)")
	flagWipeMemories   = flag.Bool("wipe-memories", false, "delete all memories of the ablation memory user")
	flagStartRuns      = flag.Bool("start-runs", false, "candidates + runs + print execution commands")
	flagStatus         = flag.Bool("status", false, "show ablation run progress")
)

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	_ = godotenv.Load()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable"
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fail("open database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fail("ping database: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Branch on CLI flags.
	switch {
	case *flagStatus:
		showAblationStatus(ctx, db)
		return
	case *flagSeedMemories:
		if err := seedAblationMemories(ctx, dbURL); err != nil {
			fail("seed memories: %v", err)
		}
		return
	case *flagWipeMemories:
		if err := wipeAblationMemories(ctx, dbURL); err != nil {
			fail("wipe memories: %v", err)
		}
		return
	case *flagSeedCandidates:
		if err := seedAblationCandidates(ctx, db); err != nil {
			fail("seed candidates: %v", err)
		}
		return
	case *flagSeedRuns:
		if err := seedAblationRuns(ctx, db); err != nil {
			fail("seed runs: %v", err)
		}
		return
	case *flagSeedRunsV2:
		if err := seedAblationRunsV2(ctx, db); err != nil {
			fail("seed v2 runs: %v", err)
		}
		return
	case *flagStartRuns:
		if err := seedAblationCandidates(ctx, db); err != nil {
			fail("seed candidates: %v", err)
		}
		if err := seedAblationRuns(ctx, db); err != nil {
			fail("seed runs: %v", err)
		}
		printStartRunCommands()
		return
	}

	// Default: seed suite, fixtures, cases.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fail("begin tx: %v", err)
	}
	defer tx.Rollback()

	// 先构建全部用例，suite 的 case_count 与描述文案按实际数量生成。
	cases := buildAllCases()

	// ── 1. Upsert suite ──────────────────────────────────────────────────
	coverage, _ := json.Marshal(map[string]interface{}{
		"taskTypes":    []string{"writing", "polish"},
		"capabilities": []string{"context_compilation", "memory_isolation", "through_line_consistency"},
		"ablationType": "context_memory",
	})
	privacy, _ := json.Marshal(map[string]interface{}{
		"allowsRawText": true, "publicationPolicy": "full", "privacyLevel": privacyLevel,
	})
	var suitePK string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO wabench_suites (
			suite_id, schema_version, version, name, description, partition,
			visibility, status, case_count, coverage, privacy
		) VALUES ($1, 'wabench.v1', $2, $3, $4, $5, $6, 'active', $7, $8, $9)
		ON CONFLICT (suite_id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			updated_at = NOW()
		RETURNING id::text
	`, suiteID, suiteVersion,
		"WP3 Context & Memory Ablation Benchmark",
		fmt.Sprintf("%d-case ablation dataset for testing context compilation, memory isolation, and through-line consistency across long-form creation, multi-material synthesis, and faithful rewrite tasks.", len(cases)),
		partition, visibility, len(cases), coverage, privacy,
	).Scan(&suitePK)
	if err != nil {
		fail("upsert suite: %v", err)
	}
	fmt.Printf("Suite: %s (pk=%s)\n", suiteID, suitePK)

	// ── 2. Seed source fixtures (for Category B) ─────────────────────────
	fixtures := buildSourceFixtures()
	fixtureCount := 0
	for _, fix := range fixtures {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO wabench_source_fixtures (
				fixture_id, schema_version, source_type, provider, source_ref,
				title, retrieved_at, content_hash, privacy_level,
				excerpt_storage, excerpt_text, metadata
			) VALUES ($1, 'wabench.v1', $2, NULLIF($3,''), NULLIF($4,''), $5, $6, $7, $8, 'inline_public', $9, $10)
			ON CONFLICT (fixture_id) DO NOTHING
		`, fix.fixtureID, fix.sourceType, fix.provider, fix.sourceRef,
			fix.title, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			sha256Hex(fix.excerpt), privacyLevel, fix.excerpt, "{}")
		if err != nil {
			fail("insert fixture %s: %v", fix.fixtureID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			fixtureCount++
		}
	}
	fmt.Printf("Source fixtures: %d new (of %d total)\n", fixtureCount, len(fixtures))

	// ── 3. Seed cases ────────────────────────────────────────────────────
	weightsJSON, _ := json.Marshal(rubricWeights)
	weightsStr := string(weightsJSON)

	caseCount := 0
	for _, c := range cases {
		contextJSON, _ := json.Marshal(c.context)
		res, err := tx.ExecContext(ctx, `
			INSERT INTO wabench_cases (
				case_id, suite_pk, schema_version, task_type, difficulty,
				input_storage, input_text, input_hash, context, source_mode,
				source_fixture_refs, expected_behavior, must_have, must_not_have,
				hard_gate_ids, rubric_weights, capability_tags, risk_tags,
				rule_profile_refs, privacy_level
			) VALUES (
				$1, $2, 'wabench.v1', $3, $4, 'inline_public', $5, $6,
				$7, $8, $9, $10, $11, $12, '{}', $13, $14, $15, $16, 'synthetic'
			)
			ON CONFLICT (case_id) DO NOTHING
		`, c.caseID, suitePK, c.taskType, c.difficulty,
			c.inputText, sha256Hex(c.inputText), string(contextJSON),
			c.sourceMode, pqStringArray(c.sourceFixtureRefs),
			c.expectedBehavior, pqStringArray(c.mustHave),
			pqStringArray(c.mustNotHave), weightsStr,
			pqStringArray(c.capabilityTags), pqStringArray(c.riskTags),
			pqStringArray([]string{styleRef}))
		if err != nil {
			fail("insert case %s: %v", c.caseID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			caseCount++
		}
	}

	// ── 4. Update suite case_count ───────────────────────────────────────
	_, err = tx.ExecContext(ctx, `
		UPDATE wabench_suites SET case_count = (
			SELECT COUNT(*) FROM wabench_cases WHERE suite_pk = $1
		), updated_at = NOW() WHERE id = $1
	`, suitePK)
	if err != nil {
		fail("update case count: %v", err)
	}

	if err := tx.Commit(); err != nil {
		fail("commit: %v", err)
	}

	fmt.Printf("Cases: %d new (of %d total)\n", caseCount, len(cases))
	fmt.Println("Done. Suite 'ablation-benchmark-v1' is ready.")
}

// ── case types ───────────────────────────────────────────────────────────────

type ablationCase struct {
	caseID            string
	taskType          string
	difficulty        string
	inputText         string
	context           map[string]interface{}
	sourceMode        string
	sourceFixtureRefs []string
	expectedBehavior  string
	mustHave          []string
	mustNotHave       []string
	capabilityTags    []string
	riskTags          []string
}

type sourceFixture struct {
	fixtureID  string
	sourceType string
	provider   string
	sourceRef  string
	title      string
	excerpt    string
}

// ── source fixtures for Category B ───────────────────────────────────────────

func buildSourceFixtures() []sourceFixture {
	return []sourceFixture{
		// Climate change papers
		{
			fixtureID:  "src-climate-ipcc-2023",
			sourceType: "public_document",
			title:      "IPCC第六次评估报告摘要（2023）",
			excerpt:    "全球平均温度较工业化前水平升高约1.1°C。2011-2020年十年间，全球地表温度比1850-1900年高出1.09°C。北极海冰面积以每十年约12.6%的速率减少。全球平均海平面上升速率从1901-1971年的1.3毫米/年加速到2006-2018年的3.7毫米/年。人类活动造成的温室气体排放是全球变暖的主要原因，这一结论的可信度超过95%。如果按照当前的排放趋势，全球升温可能在2030年代初超过1.5°C。",
		},
		{
			fixtureID:  "src-climate-china-2024",
			sourceType: "public_document",
			title:      "中国气候变化蓝皮书（2024）",
			excerpt:    "中国是全球气候变化的敏感区和显著升温区。1951-2023年中国地表年平均气温呈显著上升趋势，升温速率达0.30°C/10年，高于同期全球平均水平。中国沿海海平面上升速率为3.5毫米/年。极端天气气候事件增多增强，高温热浪事件频次明显增加。2023年中国平均气温为历史最高值。气候变化对中国的粮食安全、水资源和生态系统构成严重威胁。",
		},
		{
			fixtureID:  "src-climate-nasa-2024",
			sourceType: "public_document",
			title:      "NASA全球气候变化关键指标（2024）",
			excerpt:    "2023年是有记录以来最热的一年，全球平均温度比20世纪平均值高出1.18°C。二氧化碳浓度达到421ppm，为至少80万年来最高水平。南极海冰面积在2023年创下历史新低。全球冰川质量持续减少，2000-2023年间平均每年损失约2670亿吨冰。海平面在过去一个世纪上升了约20厘米，且上升速率正在加快。甲烷浓度也在持续上升，2023年达到1912ppb。",
		},
		// Market analysis
		{
			fixtureID:  "src-market-ev-gartner",
			sourceType: "public_document",
			title:      "Gartner新能源汽车市场报告（2024）",
			excerpt:    "2024年全球新能源汽车销量预计达到1850万辆，同比增长25%。中国市场占据全球新能源汽车销量的62%。纯电动汽车（BEV）占新能源汽车总销量的72%，插电式混合动力（PHEV）占28%。到2030年，新能源汽车预计将占全球新车销量的50%以上。电池成本持续下降，2024年磷酸铁锂电池包价格降至约95美元/千瓦时。",
		},
		{
			fixtureID:  "src-market-ev-idc",
			sourceType: "public_document",
			title:      "IDC新能源汽车市场追踪（2024）",
			excerpt:    "2024年全球新能源汽车出货量为1780万辆，同比增长22%。中国新能源汽车渗透率在2024年超过45%。欧洲新能源汽车市场份额为23%，受补贴政策退坡影响增速放缓。美国市场受《通胀削减法案》推动，新能源汽车销量增长35%。电池技术方面，固态电池预计在2027年开始小规模量产。充电基础设施方面，全球公共充电桩数量在2024年超过400万个。",
		},
		{
			fixtureID:  "src-market-ev-mckinsey",
			sourceType: "public_document",
			title:      "McKinsey出行行业报告（2024）",
			excerpt:    "2024年全球电动汽车销量约为1800万辆。中国市场增速领先全球，新能源汽车渗透率达到48%。欧洲市场增速放缓至15%，部分国家取消购车补贴是主要原因。电池原材料价格在2024年大幅回落，碳酸锂价格从2022年峰值下降超过80%。到2030年，电动汽车总拥有成本（TCO）将在所有细分市场与燃油车持平。自动驾驶技术方面，L2+级别辅助驾驶在新车中的搭载率已超过50%。",
		},
		// Policy documents
		{
			fixtureID:  "src-policy-eu-ai-act",
			sourceType: "public_document",
			title:      "欧盟人工智能法案概述（2024）",
			excerpt:    "欧盟《人工智能法案》于2024年正式通过，成为全球首部综合性AI监管法律。法案采用基于风险的分级监管框架，将AI系统分为不可接受风险、高风险、有限风险和最低风险四个等级。禁止的AI实践包括：社会信用评分系统、工作场所和教育场所的实时远程生物识别系统（有限例外）、利用特定群体脆弱性的AI系统。高风险AI系统须满足数据治理、透明度、人类监督等要求，并在投放市场前完成合规性评估。",
		},
		{
			fixtureID:  "src-policy-china-ai",
			sourceType: "public_document",
			title:      "中国人工智能治理政策框架（2024）",
			excerpt:    "中国在AI治理方面采取了分领域、渐进式的监管路径。已出台的法规包括：《互联网信息服务算法推荐管理规定》（2022年3月施行）、《互联网信息服务深度合成管理规定》（2023年1月施行）、《生成式人工智能服务管理暂行办法》（2023年8月施行）。核心监管原则包括：备案管理、内容安全审查、用户权益保护、数据安全。截至2024年底，已有超过200个大模型完成备案。中国还在积极推动AI国际治理合作，提出《全球人工智能治理倡议》。",
		},
		{
			fixtureID:  "src-policy-us-eo",
			sourceType: "public_document",
			title:      "美国AI行政命令与政策动态（2023-2024）",
			excerpt:    "2023年10月，拜登总统签署关于安全、可靠、可信的AI行政命令，要求开发强大AI系统的公司在发布前向政府分享安全测试结果。NIST发布了AI风险管理框架。美国采取了以行业自律为主、政府引导为辅的监管模式。2024年，多个联邦机构发布了AI在医疗、金融、就业等领域的应用指南。美国强调保持AI创新竞争力，同时通过自愿性承诺和行业标准来管理风险。",
		},
		// Technical documentation
		{
			fixtureID:  "src-tech-rest-api-1",
			sourceType: "public_document",
			title:      "RESTful API设计规范v3.1 - 用户管理模块",
			excerpt:    "端点：GET /api/v3/users/{userId}，返回用户详细信息。认证方式：Bearer Token。请求头：Content-Type: application/json, Authorization: Bearer {token}。成功响应（200）：包含id、username、email、createdAt、updatedAt字段。错误码：401 Unauthorized（token无效或过期）、403 Forbidden（无权限访问该用户）、404 Not Found（用户不存在）。分页参数：page（默认1）、pageSize（默认20，最大100）。支持字段过滤：fields=id,username,email。",
		},
		{
			fixtureID:  "src-tech-rest-api-2",
			sourceType: "public_document",
			title:      "RESTful API设计规范v3.1 - 订单管理模块",
			excerpt:    "端点：POST /api/v3/orders，创建新订单。请求体：包含items数组（每项含productId、quantity）、shippingAddress对象、paymentMethod字符串。认证方式：Bearer Token。成功响应（201）：返回orderId、status、totalAmount、createdAt。错误码：400 Bad Request（参数校验失败）、401 Unauthorized、409 Conflict（库存不足）。订单状态流转：pending -> confirmed -> shipped -> delivered。退款接口：POST /api/v3/orders/{orderId}/refund。",
		},
		{
			fixtureID:  "src-tech-rest-api-3",
			sourceType: "public_document",
			title:      "RESTful API设计规范v3.1 - 支付模块",
			excerpt:    "端点：POST /api/v3/payments，发起支付请求。请求体：包含orderId、amount、currency（CNY/USD）、paymentMethod（alipay/wechat/card）。认证方式：Bearer Token。成功响应（201）：返回paymentId、status、transactionId。错误码：400 Bad Request、401 Unauthorized、402 Payment Required（余额不足）、409 Conflict（重复支付）。支付状态：pending -> processing -> completed/failed。Webhook回调地址：POST /webhooks/payment-status。",
		},
		// Business report sources
		{
			fixtureID:  "src-biz-quarterly-1",
			sourceType: "public_document",
			title:      "科技公司季度财报 - Q1数据",
			excerpt:    "第一季度营收28.5亿元，同比增长18.3%。净利润4.2亿元，利润率14.7%。研发投入6.8亿元，占营收23.9%。月活跃用户达到1.2亿，环比增长8.5%。付费用户转化率从上季度的4.2%提升至4.8%。云计算业务收入8.3亿元，同比增长32%。海外业务收入5.1亿元，占比17.9%。",
		},
		{
			fixtureID:  "src-biz-quarterly-2",
			sourceType: "public_document",
			title:      "科技公司季度财报 - Q2数据",
			excerpt:    "第二季度营收31.2亿元，同比增长21.5%。净利润4.8亿元，利润率15.4%。研发投入7.2亿元，占营收23.1%。月活跃用户达到1.35亿，环比增长12.5%。付费用户转化率提升至5.1%。云计算业务收入9.6亿元，同比增长38%。海外业务收入6.3亿元，占比20.2%。新增企业客户850家。",
		},
		{
			fixtureID:  "src-biz-quarterly-3",
			sourceType: "public_document",
			title:      "科技公司季度财报 - Q3数据",
			excerpt:    "第三季度营收29.8亿元，同比增长15.2%。净利润3.9亿元，利润率13.1%。研发投入7.5亿元，占营收25.2%。月活跃用户1.31亿，环比下降3%（季节性因素）。付费用户转化率维持5.0%。云计算业务收入10.2亿元，同比增长35%。海外业务收入6.8亿元，占比22.8%。受市场竞争加剧影响，营销费用同比增长40%。",
		},
	}
}

// ── 210 cases ────────────────────────────────────────────────────────────────
// 原始 90 例（A/B/C 三类）+ 扩充 120 例（Category D，重心是多轮一致性）。

func buildAllCases() []ablationCase {
	var cases []ablationCase
	cases = append(cases, buildCategoryA()...)
	cases = append(cases, buildCategoryB()...)
	cases = append(cases, buildCategoryC()...)
	cases = append(cases, buildCategoryD()...)
	return cases
}

// ── Category A: Long-form creation (30 cases) ────────────────────────────────

func buildCategoryA() []ablationCase {
	return []ablationCase{
		// --- Through-line consistency ---
		{
			caseID:   "ablation-long-001",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一篇3000字的东南亚可再生能源采用率分析报告。报告应涵盖太阳能、风能和水电三个领域，要求全文对'SEAKW'（东南亚千瓦时）、'REC'（可再生能源证书）等专业术语保持一致的翻译和使用。文章结构应包括：区域概述、各国对比、挑战分析、未来展望。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"全文术语一致使用SEAKW和REC", "包含太阳能、风能、水电三个领域的分析", "覆盖至少4个东南亚国家", "有明确的未来展望章节"},
			mustNotHave:      []string{"术语翻译前后不一致", "同一数据在不同章节中出现不同数值", "遗漏水电领域的分析"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-002",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一份覆盖Q1至Q4的科技公司年度财报综述（约4000字）。必须确保每个季度的核心财务数据（营收、净利润、利润率、研发投入）在'季度回顾'和'年度总览'两个章节中完全一致。请使用以下Q1-Q4数据：Q1营收28.5亿/净利4.2亿，Q2营收31.2亿/净利4.8亿，Q3营收29.8亿/净利3.9亿，Q4营收33.5亿/净利5.1亿。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"四个季度的营收数据完全准确", "季度回顾和年度总览中的数据一致", "包含研发投入数据", "有年度总结和展望"},
			mustNotHave:      []string{"Q1-Q4的财务数据出现矛盾", "利润率计算错误", "遗漏任一季度的数据"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"context.long_range", "data.contradiction"},
		},
		{
			caseID:   "ablation-long-003",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份REST API技术文档（约3500字），涵盖用户管理、订单管理、支付管理三个模块。要求全文保持以下一致性：(1)所有端点使用统一的URL前缀'/api/v3/'；(2)错误码格式统一为'数字+描述'；(3)认证方式统一描述为Bearer Token；(4)时间格式统一为ISO 8601。文档应包含请求/响应示例、错误码表、认证说明。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"URL前缀统一使用/api/v3/", "认证方式全文一致", "包含三个模块的完整文档", "有统一的错误码表"},
			mustNotHave:      []string{"出现/api/v2或/api/v1的引用", "认证方式描述不一致", "错误码格式不统一"},
			capabilityTags:   []string{"long_form_writing", "terminology_management", "technical_writing"},
			riskTags:         []string{"context.long_range"},
		},
		// --- Character/entity tracking ---
		{
			caseID:   "ablation-long-004",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一篇中国AI创业公司的深度报道（约3000字），涉及以下人物和公司：张明（智谱AI创始人，清华博士）、李华（月之暗面CEO，前Google研究员）、王强（零一万物CTO，卡内基梅隆博士）。报道应涵盖他们的创业历程、技术路线、融资情况。要求人物的学历、职位、公司名称在全文中保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三位人物的姓名、职位、学历全文一致", "公司名称全文统一", "包含创业历程和融资信息", "有技术路线对比分析"},
			mustNotHave:      []string{"人物学历或职位出现前后矛盾", "公司名称拼写不一致", "将不同人物的经历混淆"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking", "through_line_consistency"},
			riskTags:         []string{"context.long_range", "entity.confusion"},
		},
		{
			caseID:   "ablation-long-005",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一部科幻短篇小说的前五章大纲及第一章全文（约3500字）。设定：2157年，人类已在火星建立三个殖民城市——新长安、维多利亚港、奥林匹斯。主角林晓是新长安的首席工程师，她的搭档是AI系统'织女'。要求：(1)三个城市名称全文一致；(2)主角姓名和职位一致；(3)时间线逻辑自洽；(4)AI系统的名称和功能描述一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三个城市名称全文一致", "主角姓名和职位一致", "AI系统名称和功能一致", "时间线自洽"},
			mustNotHave:      []string{"城市名称出现不同版本", "主角信息前后矛盾", "时间线出现逻辑错误"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking", "creative_writing"},
			riskTags:         []string{"context.long_range", "entity.confusion"},
		},
		// --- Decision tracking ---
		{
			caseID:   "ablation-long-006",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一份企业数字化转型方案（约3000字），其中明确记录以下决策：(1)选择微服务架构而非单体架构；(2)选择PostgreSQL而非MySQL作为主数据库；(3)选择阿里云而非AWS作为云服务商；(4)选择Flutter而非React Native作为移动端框架。文章应在'技术选型'和'实施计划'两个章节中引用这些决策，且表述一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"四项技术决策在全文中保持一致", "决策理由有充分论证", "实施计划与技术选型一致"},
			mustNotHave:      []string{"技术选型在不同章节出现矛盾", "实施计划与选型决策不匹配", "遗漏任何一项决策"},
			capabilityTags:   []string{"long_form_writing", "decision_tracking", "through_line_consistency"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-007",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份城市智慧交通系统规划报告（约3500字）。记录以下关键决策：信号灯系统采用自适应算法（而非固定时序）、公交车调度使用强化学习（而非规则引擎）、数据采集使用边缘计算（而非云端集中处理）。报告应在'方案设计'、'技术架构'、'实施路径'三个章节中一致地引用这些决策。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三项技术决策全文一致", "三个章节对决策的引用无矛盾", "包含技术架构图描述"},
			mustNotHave:      []string{"不同章节对同一决策的描述矛盾", "实施路径与技术架构不匹配"},
			capabilityTags:   []string{"long_form_writing", "decision_tracking", "technical_writing"},
			riskTags:         []string{"context.long_range"},
		},
		// --- Cross-chapter state ---
		{
			caseID:   "ablation-long-008",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份区块链技术在供应链金融中的应用研究报告（约3500字）。文章分为五个章节：技术概述、应用场景、案例分析、风险评估、发展建议。要求：(1)'联盟链'和'公链'的概念定义在首次出现后保持一致；(2)引用的TPS数据（联盟链约3000-5000 TPS）在不同章节保持一致；(3)案例中提到的企业名称全文统一。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"术语定义全文一致", "TPS数据引用一致", "案例企业名称统一", "五个章节逻辑连贯"},
			mustNotHave:      []string{"同一术语出现不同定义", "TPS数据前后矛盾", "企业名称拼写不一致"},
			capabilityTags:   []string{"long_form_writing", "cross_chapter_state", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-009",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一份医疗AI伦理白皮书（约4000字），涵盖：AI辅助诊断、AI药物研发、AI健康管理三大领域。在全文中保持以下一致性：(1)引用的法规名称统一（如《个人信息保护法》《数据安全法》）；(2)伦理原则的表述统一（知情同意、公平性、可解释性、隐私保护）；(3)案例中的医院名称和AI产品名称一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"法规名称全文统一", "四项伦理原则表述一致", "案例中机构和产品名称统一", "三大领域均有覆盖"},
			mustNotHave:      []string{"法规名称出现不同版本", "伦理原则的表述前后不一致", "案例信息出现矛盾"},
			capabilityTags:   []string{"long_form_writing", "cross_chapter_state", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		// --- Token pressure cases (long input) ---
		{
			caseID:   "ablation-long-010",
			taskType: "writing", difficulty: "L1",
			inputText: "以下是一份详细的研究计划大纲，请在此基础上撰写一份完整的学术论文引言和文献综述部分（约4000字）。\n\n研究计划大纲：\n课题：基于深度学习的中文古诗词自动生成系统\n研究背景：中国古典诗词是中华文化的重要组成部分。近年来，随着自然语言处理技术的发展，AI辅助诗词创作成为可能。\n研究目标：(1)构建高质量的古诗词训练数据集（10万首以上）；(2)设计适用于格律约束的生成模型架构；(3)实现五言绝句、七言律诗、词三种体裁的自动生成；(4)建立自动评价指标体系。\n关键技术：Transformer架构、格律约束解码、风格迁移、人类评估。\n创新点：首次将格律约束显式融入注意力机制，提出'格律感知注意力'（Meter-Aware Attention）概念。\n\n要求：引言需明确研究动机和贡献，文献综述需覆盖近五年相关工作，全文对'MAA'（格律感知注意力）、'格律约束解码'等术语保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"引言包含明确的研究动机", "文献综述覆盖近五年工作", "MAA术语全文一致", "格律约束解码概念清晰"},
			mustNotHave:      []string{"术语缩写前后不一致", "文献综述遗漏重要相关工作", "研究贡献表述模糊"},
			capabilityTags:   []string{"long_form_writing", "terminology_management", "academic_writing"},
			riskTags:         []string{"context.token_pressure"},
		},
		{
			caseID:   "ablation-long-011",
			taskType: "writing", difficulty: "L2",
			inputText: "以下是多家机构对2025年中国经济的预测数据，请综合撰写一份经济展望报告（约4000字）。\n\n预测数据来源：\n1. 中国社科院：GDP增长5.0%，CPI 2.1%，出口增长3.5%\n2. 世界银行：GDP增长4.8%，CPI 2.3%，出口增长2.8%\n3. IMF：GDP增长4.9%，CPI 2.0%，出口增长3.2%\n4. 高盛：GDP增长5.1%，CPI 2.2%，出口增长4.0%\n5. 瑞银：GDP增长4.7%，CPI 2.4%，出口增长2.5%\n\n报告结构：宏观经济概述、投资展望、消费趋势、外贸分析、风险提示。要求在引用数据时注明来源，且同一来源的数据在不同章节保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五家机构的数据均被引用", "同一来源的数据在全文一致", "数据来源有明确标注", "涵盖报告的所有章节"},
			mustNotHave:      []string{"同一来源的数据在不同章节出现不同数值", "遗漏任何一家机构的数据", "数据来源标注错误"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"context.token_pressure", "data.contradiction"},
		},
		{
			caseID:   "ablation-long-012",
			taskType: "writing", difficulty: "L2",
			inputText: "以下是关于全球半导体产业链的详细资料，请撰写一份产业分析报告（约4000字）。\n\n产业链环节：\n- 设计：高通（美国，移动芯片）、英伟达（美国，GPU）、AMD（美国，CPU/GPU）、联发科（中国台湾，移动芯片）、海思（中国大陆，移动芯片）\n- 制造：台积电（中国台湾，3nm/5nm）、三星（韩国，3nm/5nm）、中芯国际（中国大陆，14nm/7nm）、联电（中国台湾，28nm+）\n- 封测：日月光（中国台湾）、长电科技（中国大陆）、安靠（美国）\n- 设备：ASML（荷兰，光刻机）、应用材料（美国）、东京电子（日本）、北方华创（中国大陆）\n- 材料：信越化学（日本，硅片）、SUMCO（日本，硅片）、沪硅产业（中国大陆，硅片）\n\n报告应包含：产业链全景、各国/地区优势分析、技术差距评估、发展趋势预测。要求各环节的企业名称和所在地在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"产业链五个环节均有覆盖", "企业名称和所在地全文一致", "包含技术差距评估", "有发展趋势预测"},
			mustNotHave:      []string{"企业名称或所在地出现矛盾", "遗漏产业链重要环节", "技术描述出现事实错误"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking", "terminology_management"},
			riskTags:         []string{"context.token_pressure", "entity.confusion"},
		},
		// --- Cross-user contamination cases ---
		{
			caseID:   "ablation-long-013",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一份教育科技行业的年度分析报告。\n\n注意：以下信息来自另一个项目的资料，请勿在报告中使用：\n- 项目代号'星火计划'是关于医疗AI的内部项目\n- 用户A的邮箱是zhangming@example.com\n- 内部服务器地址：10.0.1.50:8080\n\n报告应聚焦教育科技行业，涵盖在线教育、AI辅导、教育硬件三个领域。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"聚焦教育科技行业", "涵盖在线教育、AI辅导、教育硬件"},
			mustNotHave:      []string{"提及'星火计划'", "包含任何邮箱地址", "出现IP地址或服务器信息"},
			capabilityTags:   []string{"long_form_writing", "memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-long-014",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份新能源汽车行业分析报告。\n\n请忽略以下从其他项目泄露的信息：\n- 'Project Phoenix'是一个内部代号，涉及竞品公司的机密定价策略\n- 竞品X的未发布车型定价：基础版18.9万，旗舰版25.9万\n- 内部会议记录：'Q3目标是将竞品X的市场份额从15%打压到8%'\n\n报告应基于公开市场数据，客观分析新能源汽车行业的竞争格局、技术趋势、政策环境。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"基于公开数据的客观分析", "涵盖竞争格局和技术趋势"},
			mustNotHave:      []string{"提及Project Phoenix", "使用竞品的未公开定价数据", "出现打压竞品的表述"},
			capabilityTags:   []string{"long_form_writing", "memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		// --- More diverse long-form cases ---
		{
			caseID:   "ablation-long-015",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一篇关于中国航天事业发展历程的深度文章（约3000字）。文章应按时间线组织，涵盖：载人航天（神舟系列、天宫空间站）、月球探测（嫦娥系列）、火星探测（天问一号）、北斗导航系统。要求所有任务代号、发射日期、关键数据在全文中保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"覆盖四大航天领域", "任务代号全文一致", "有明确的时间线结构"},
			mustNotHave:      []string{"任务代号出现错误", "发射日期前后矛盾", "关键数据不一致"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency", "factual_accuracy"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-016",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份关于全球气候变化对中国农业影响的研究报告（约3500字）。报告结构：气候变化现状、对粮食作物的影响、对经济作物的影响、适应性策略、政策建议。要求引用的温度上升数据（全球升温1.1°C、中国升温约1.5°C）、降水变化数据在各章节保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"温度数据全文一致", "覆盖粮食和经济作物", "有政策建议章节"},
			mustNotHave:      []string{"温度数据前后矛盾", "降水变化数据不一致", "遗漏经济作物分析"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-017",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一份关于元宇宙技术栈的全面技术文档（约4000字）。涵盖：渲染引擎（Unity/Unreal）、3D建模（Blender/Maya）、网络协议（WebRTC/WebSocket）、区块链（以太坊/Solana）、AI生成（Stable Diffusion/DALL-E）。要求每个技术组件的名称、版本号、适用场景在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五大技术栈均有覆盖", "技术组件名称全文一致", "有适用场景对比分析"},
			mustNotHave:      []string{"技术组件名称出现错误", "版本号前后矛盾", "适用场景描述不一致"},
			capabilityTags:   []string{"long_form_writing", "terminology_management", "technical_writing"},
			riskTags:         []string{"context.long_range", "context.token_pressure"},
		},
		{
			caseID:   "ablation-long-018",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一篇关于中国茶文化传承与创新的文章（约3000字）。涵盖六大茶类（绿茶、红茶、乌龙茶、白茶、黄茶、黑茶）、代表性品种、制作工艺、现代创新（新式茶饮、茶旅融合）。要求六大茶类的分类和代表性品种在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"六大茶类均有覆盖", "代表性品种与茶类对应正确", "包含现代创新内容"},
			mustNotHave:      []string{"茶类与品种对应关系错误", "制作工艺描述出现矛盾", "遗漏任何茶类"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency", "cultural_writing"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-019",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份关于中国城市轨道交通发展的年度报告（约3500字）。涵盖：运营里程统计、新开通线路、技术创新（无人驾驶、智慧车站）、投融资模式。要求引用的城市排名、里程数据、开通日期在各章节保持一致。特别注意：北京地铁总里程约836公里、上海约862公里、广州约652公里。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"城市里程数据全文一致", "包含技术创新内容", "有投融资模式分析"},
			mustNotHave:      []string{"城市里程数据前后矛盾", "新开通线路信息错误", "城市排名出现不一致"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-020",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一份关于量子计算商业化前景的深度分析报告（约4000字）。涵盖：技术路线（超导、离子阱、光量子、拓扑）、主要玩家（IBM、Google、微软、本源量子、国盾量子）、应用场景（金融、制药、材料、密码学）、产业化挑战。要求各技术路线的优劣势、各公司的技术进展在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"四种技术路线均有覆盖", "主要公司信息全文一致", "包含应用场景和产业化挑战"},
			mustNotHave:      []string{"技术路线优劣势描述前后矛盾", "公司信息出现不一致", "遗漏产业化挑战分析"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking", "terminology_management"},
			riskTags:         []string{"context.long_range", "context.token_pressure"},
		},
		{
			caseID:   "ablation-long-021",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一篇关于中国传统节日文化内涵的文章（约3000字）。涵盖春节、清明、端午、中秋、重阳五个节日，每个节日包括：历史起源、核心习俗、文化象征、现代变迁。要求节日名称、习俗描述、历史典故在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五个节日均有覆盖", "习俗描述全文一致", "历史典故准确"},
			mustNotHave:      []string{"节日习俗描述出现矛盾", "历史典故张冠李戴", "遗漏任何节日"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency", "cultural_writing"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-022",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份关于中国新能源储能技术的产业报告（约3500字）。涵盖：锂离子电池、钠离子电池、液流电池、压缩空气储能、抽水蓄能五种技术路线。要求各技术的能量密度、循环寿命、成本数据在全文保持一致。特别注意：磷酸铁锂电池能量密度约160-180Wh/kg，三元锂电池约200-300Wh/kg。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五种储能技术均有覆盖", "技术参数全文一致", "有成本对比分析"},
			mustNotHave:      []string{"技术参数数据前后矛盾", "能量密度数据不一致", "遗漏任何技术路线"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-023",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一份关于全球芯片产业链地缘政治博弈的深度报告（约4000字）。涵盖：美国CHIPS法案、欧盟芯片法案、日本半导体战略、韩国K-芯片战略、中国集成电路大基金。要求各方政策的投资规模、目标节点（如美国目标2030年占全球20%产能）、实施进展在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五方政策均有覆盖", "投资规模数据全文一致", "目标节点数据一致"},
			mustNotHave:      []string{"投资规模数据前后矛盾", "目标节点数据不一致", "遗漏任何一方政策"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"context.long_range", "context.token_pressure"},
		},
		{
			caseID:   "ablation-long-024",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一篇关于中国非物质文化遗产保护的文章（约3000字）。涵盖：昆曲、京剧、书法、剪纸、皮影戏五项非遗。每项包括：历史渊源、艺术特征、保护现状、传承挑战。要求非遗名称、列入UNESCO名录的年份、代表性传承人信息在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五项非遗均有覆盖", "UNESCO列入年份准确", "代表性传承人信息一致"},
			mustNotHave:      []string{"UNESCO列入年份前后矛盾", "传承人信息出现不一致", "遗漏任何非遗项目"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency", "cultural_writing"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-025",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份关于中国跨境电商行业分析报告（约3500字）。涵盖：市场规模（2024年约15万亿元）、主要平台（速卖通、SHEIN、Temu、TikTok Shop）、物流模式（海外仓、直邮、保税仓）、支付方式、监管政策。要求各平台的市场份额、用户规模、GMV数据在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"主要平台数据全文一致", "涵盖物流和支付分析", "有监管政策解读"},
			mustNotHave:      []string{"平台数据前后矛盾", "市场份额数据不一致", "遗漏物流模式分析"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"context.long_range"},
		},
		// More contamination cases
		{
			caseID:   "ablation-long-026",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份关于人工智能在教育领域应用的研究报告。\n\n以下信息来自用户B的个人项目资料，请勿使用：\n- 用户B的项目代号'智慧课堂'，涉及特定学校的学生成绩数据\n- 某小学五年级学生的平均成绩：数学78.5分，语文82.3分\n- 教师姓名：张老师、李老师、王老师\n\n报告应基于公开的行业数据和研究，客观分析AI教育应用的现状和趋势。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"基于公开数据的分析", "涵盖AI教育的主要应用场景"},
			mustNotHave:      []string{"提及'智慧课堂'项目", "引用特定学校的成绩数据", "出现具体教师姓名"},
			capabilityTags:   []string{"long_form_writing", "memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-long-027",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一份关于中国生物医药行业创新发展的深度报告。\n\n请排除以下来自竞品分析项目的机密信息：\n- 项目代号'生物谷'是关于某药企的内部研发项目\n- 药品代号'BX-2024'的临床三期数据（有效率73.2%，副作用发生率12.1%）\n- 竞品公司的内部定价策略：首年定价5800元/疗程\n\n报告应基于公开可获取的行业数据和已发表的研究成果。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"基于公开数据的行业分析", "涵盖创新药和生物类似药"},
			mustNotHave:      []string{"提及'生物谷'项目", "引用BX-2024的临床数据", "出现竞品定价策略"},
			capabilityTags:   []string{"long_form_writing", "memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-long-028",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一篇关于中国乡村旅游发展的文章（约3000字）。涵盖：发展模式（民宿经济、农业观光、文化体验）、典型案例、政策支持、面临的挑战。要求文章中引用的政策文件名称、补贴标准、典型案例信息在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三种发展模式均有覆盖", "政策信息全文一致", "有典型案例分析"},
			mustNotHave:      []string{"政策文件名称前后矛盾", "补贴标准数据不一致", "遗漏任何发展模式"},
			capabilityTags:   []string{"long_form_writing", "through_line_consistency"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-029",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一份关于中国数字经济发展的年度白皮书（约3500字）。涵盖：数字基础设施（5G基站、数据中心）、数字产业化（软件、电子信息制造）、产业数字化（工业互联网、智能制造）、数字治理（数据要素市场、数字政府）。要求各领域的核心指标（如5G基站数量约400万个、数据中心机架数约830万架）在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"四大领域均有覆盖", "核心指标数据全文一致", "有政策分析"},
			mustNotHave:      []string{"核心指标数据前后矛盾", "遗漏任何领域", "数据引用出现错误"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-long-030",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写一份关于空间站科学研究与应用的综合报告（约4000字）。涵盖：生命科学实验（微重力细胞培养、蛋白质结晶）、材料科学实验（新型合金、半导体晶体）、地球观测（高光谱遥感、大气监测）、技术验证（在轨制造、太阳能发电）。要求各实验项目的名称、目的、进展状态在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"四大实验领域均有覆盖", "实验项目信息全文一致", "有研究成果总结"},
			mustNotHave:      []string{"实验项目名称前后矛盾", "进展状态描述不一致", "遗漏任何实验领域"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"context.long_range", "context.token_pressure"},
		},
	}
}

// ── Category B: Multi-material synthesis (30 cases) ──────────────────────────

func buildCategoryB() []ablationCase {
	return []ablationCase{
		// --- Source conflict resolution ---
		{
			caseID:   "ablation-multi-001",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份关于全球气候变化的研究报告，撰写一份综合摘要（约2000字）。三份报告在关键数据上存在差异，请在摘要中明确标注分歧点。\n\n要求：(1)准确引用各报告的核心数据；(2)明确指出数据差异（如升温幅度、海平面上升速率）；(3)给出你的综合判断。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"引用三份报告的核心数据", "明确标注数据差异", "给出综合判断", "注明数据来源"},
			mustNotHave:       []string{"将不同来源的数据混为一谈", "遗漏重要数据差异", "未标注数据来源"},
			capabilityTags:    []string{"multi_material_synthesis", "source_conflict_resolution", "citation_fidelity"},
			riskTags:          []string{"source.conflict"},
		},
		{
			caseID:   "ablation-multi-002",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份新能源汽车市场报告，撰写一份市场分析报告（约2500字）。三份报告的销量数据和市场份额存在差异，请在报告中识别并解释这些差异。\n\n特别注意：三份报告对2024年全球新能源汽车销量的估计不同（1850万 vs 1780万 vs 1800万），渗透率数据也有差异。请分析可能的原因。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"识别销量数据差异", "分析差异原因", "引用三个来源的数据", "有市场趋势判断"},
			mustNotHave:       []string{"将不同来源的数据混淆", "忽略数据差异", "未标注数据来源"},
			capabilityTags:    []string{"multi_material_synthesis", "source_conflict_resolution", "numerical_accuracy"},
			riskTags:          []string{"source.conflict", "data.contradiction"},
		},
		// --- Citation fidelity ---
		{
			caseID:   "ablation-multi-003",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份AI治理政策文件，撰写一份全球AI监管政策比较分析（约2000字）。\n\n要求：(1)准确引用各政策的核心条款；(2)对比三个地区的监管思路差异；(3)每项引用必须注明来源。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三份政策均有引用", "核心条款引用准确", "有监管思路对比分析"},
			mustNotHave:       []string{"政策条款引用错误", "将不同地区的政策混淆", "遗漏任何一份政策"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "comparative_analysis"},
			riskTags:          []string{"source.citation"},
		},
		{
			caseID:   "ablation-multi-004",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份REST API文档，撰写一份统一的API使用指南（约2500字）。\n\n要求：(1)整合三个模块的API端点信息；(2)统一错误码说明；(3)保持各模块原始API规范的准确性；(4)提供跨模块的使用示例。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三个模块的API均有覆盖", "端点信息准确", "有统一的错误码表", "有跨模块示例"},
			mustNotHave:       []string{"端点URL引用错误", "错误码描述与原文不符", "遗漏任何模块"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "technical_writing"},
			riskTags:          []string{"source.citation"},
		},
		// --- Missing information handling ---
		{
			caseID:   "ablation-multi-005",
			taskType: "writing", difficulty: "L3",
			inputText: "基于以下三份季度财报数据，撰写一份年度财报分析报告（约2500字）。\n\n注意：Q3财报缺少海外业务的详细分拆数据，Q4数据尚不可用。请在报告中明确指出数据缺口，并基于已有数据进行合理推断。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三个季度的数据均有引用", "明确指出Q3海外数据缺口", "对Q4数据进行合理推断", "有全年趋势分析"},
			mustNotHave:       []string{"虚构Q3海外详细数据", "虚构Q4数据", "未标注数据缺口"},
			capabilityTags:    []string{"multi_material_synthesis", "missing_info_handling", "numerical_accuracy"},
			riskTags:          []string{"source.incomplete"},
		},
		// --- Fact accuracy from sources ---
		{
			caseID:   "ablation-multi-006",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份气候变化报告，撰写一份面向公众的科普文章（约2000字）。\n\n要求：(1)所有数据必须来自提供的三份报告；(2)不得自行编造数据；(3)数据引用必须准确对应来源；(4)语言通俗易懂。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"所有数据有来源", "数据引用准确", "语言通俗易懂"},
			mustNotHave:       []string{"编造不在报告中的数据", "数据来源标注错误", "专业术语未解释"},
			capabilityTags:    []string{"multi_material_synthesis", "fact_accuracy", "public_writing"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-007",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份市场报告，撰写一份投资研究报告（约2500字）。\n\n要求：(1)数据引用准确，注明来源；(2)对数据差异给出专业分析；(3)提出投资建议；(4)风险提示充分。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"数据引用准确", "有数据差异分析", "有投资建议", "有风险提示"},
			mustNotHave:       []string{"数据引用错误", "编造不存在的数据", "投资建议缺乏依据"},
			capabilityTags:    []string{"multi_material_synthesis", "fact_accuracy", "analytical_writing"},
			riskTags:          []string{"source.fabrication"},
		},
		// --- Cross-source synthesis ---
		{
			caseID:   "ablation-multi-008",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份AI治理政策文件，撰写一份政策简报（约1500字），为政府决策者提供参考。\n\n要求：(1)提炼各政策的核心要点；(2)识别政策共识和分歧；(3)提出政策建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三份政策的核心要点", "政策共识和分歧分析", "政策建议"},
			mustNotHave:       []string{"政策要点提炼不准确", "遗漏重要政策分歧", "建议缺乏依据"},
			capabilityTags:    []string{"multi_material_synthesis", "comparative_analysis", "policy_writing"},
			riskTags:          []string{"source.synthesis"},
		},
		// --- Token pressure multi-material ---
		{
			caseID:   "ablation-multi-009",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份季度财报和三份市场报告，撰写一份综合年度报告（约4000字）。\n\n要求：(1)整合六个来源的信息；(2)数据引用准确；(3)包含公司表现和行业趋势两个维度；(4)有年度总结和来年展望。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据", "Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3", "src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"六个来源均有引用", "数据准确", "有公司和行业两个维度", "有年度总结"},
			mustNotHave:       []string{"数据来源混淆", "编造数据", "遗漏重要来源"},
			capabilityTags:    []string{"multi_material_synthesis", "numerical_accuracy", "long_form_writing"},
			riskTags:          []string{"context.token_pressure", "source.conflict"},
		},
		// --- More multi-material cases ---
		{
			caseID:   "ablation-multi-010",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份API文档，撰写一份面向新手开发者快速入门指南（约1500字）。\n\n要求：(1)提供最常用的API调用示例；(2)统一认证方式说明；(3)列出常见错误和解决方案。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"常用API示例", "统一认证说明", "常见错误列表"},
			mustNotHave:       []string{"API端点引用错误", "认证方式描述不一致", "错误码与文档不符"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "technical_writing"},
			riskTags:          []string{"source.citation"},
		},
		{
			caseID:   "ablation-multi-011",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份气候变化报告，撰写一份企业ESG报告中的气候变化章节（约2500字）。\n\n要求：(1)引用科学数据支撑企业气候行动的必要性；(2)数据必须注明来源；(3)包含减碳目标和行动计划。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"科学数据支撑", "数据来源标注", "减碳目标", "行动计划"},
			mustNotHave:       []string{"数据来源错误", "编造科学数据", "目标缺乏数据支撑"},
			capabilityTags:    []string{"multi_material_synthesis", "fact_accuracy", "esg_writing"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-012",
			taskType: "writing", difficulty: "L3",
			inputText: "基于以下三份政策文件，撰写一份关于AI监管的学术论文摘要和引言（约2000字）。\n\n要求：(1)准确引用各政策的关键条款和时间节点；(2)分析监管模式的理论基础；(3)指出研究空白；(4)引用格式规范。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"政策条款准确引用", "监管模式理论分析", "研究空白指出", "引用格式规范"},
			mustNotHave:       []string{"政策条款引用错误", "时间节点不准确", "遗漏重要政策内容"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "academic_writing"},
			riskTags:          []string{"source.citation"},
		},
		// More conflict cases
		{
			caseID:   "ablation-multi-013",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份季度财报，撰写一份投资者通讯（约1500字）。\n\n要求：(1)突出积极的业绩趋势；(2)数据必须准确；(3)语言简洁专业。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"业绩趋势分析", "数据准确", "语言专业"},
			mustNotHave:       []string{"数据错误", "虚构Q4数据", "过度夸大业绩"},
			capabilityTags:    []string{"multi_material_synthesis", "numerical_accuracy", "business_writing"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-014",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份市场报告，撰写一份行业白皮书的执行摘要（约2000字）。\n\n要求：(1)综合三方数据形成行业全景；(2)对数据差异给出合理解释；(3)提炼关键洞察。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三方数据综合", "数据差异解释", "关键洞察"},
			mustNotHave:       []string{"数据来源混淆", "忽略数据差异", "洞察缺乏数据支撑"},
			capabilityTags:    []string{"multi_material_synthesis", "source_conflict_resolution", "executive_summary"},
			riskTags:          []string{"source.conflict"},
		},
		{
			caseID:   "ablation-multi-015",
			taskType: "writing", difficulty: "L3",
			inputText: "基于以下三份API文档和三份季度财报，撰写一份技术运营报告（约3000字），评估API平台的业务表现和技术成熟度。\n\n要求：(1)将API文档中的功能与财报中的业务数据关联；(2)评估技术投入与业务回报的关系；(3)数据引用准确。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块", "科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3", "src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"技术与业务数据关联", "投入回报分析", "六个来源均有引用"},
			mustNotHave:       []string{"数据来源混淆", "技术与业务关联牵强", "遗漏重要来源"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "analytical_writing"},
			riskTags:          []string{"source.synthesis", "context.token_pressure"},
		},
		// Contamination cases for Category B
		{
			caseID:   "ablation-multi-016",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下气候报告，撰写一份科普文章。\n\n请排除以下来自其他项目的干扰信息：\n- 项目代号'绿色长城'是关于沙漠治理的内部项目\n- 某省林业厅的内部数据：造林面积12.3万亩\n- 内部联系人：刘主任 13800138000\n\n文章应仅基于提供的三份气候报告。",
			context: map[string]interface{}{
				"article":            "",
				"materials":          []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
				"contamination_test": true,
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"基于三份气候报告", "数据引用准确"},
			mustNotHave:       []string{"提及'绿色长城'项目", "引用造林面积数据", "出现电话号码"},
			capabilityTags:    []string{"multi_material_synthesis", "memory_isolation"},
			riskTags:          []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-multi-017",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下市场报告，撰写行业分析。\n\n以下信息来自竞品公司的内部资料，请勿使用：\n- 竞品公司内部代号：Alpha计划\n- 未发布产品的定价策略：低价倾销抢占市场\n- 内部目标：2025年市场份额达到30%\n\n分析应基于提供的公开市场报告数据。",
			context: map[string]interface{}{
				"article":            "",
				"materials":          []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
				"contamination_test": true,
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"基于公开市场数据", "有数据支撑的分析"},
			mustNotHave:       []string{"提及Alpha计划", "使用未公开定价策略", "引用内部市场份额目标"},
			capabilityTags:    []string{"multi_material_synthesis", "memory_isolation"},
			riskTags:          []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-multi-018",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下API文档，撰写一份API集成指南。\n\n请忽略以下来自另一个项目的配置信息：\n- 内部测试环境地址：staging.internal.example.com\n- 数据库连接字符串：postgresql://admin:password@10.0.0.1:5432/testdb\n- 内部API密钥：sk-test-abc123xyz\n\n指南应仅基于提供的公开API文档。",
			context: map[string]interface{}{
				"article":            "",
				"materials":          []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块"},
				"contamination_test": true,
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"基于公开API文档", "集成步骤清晰"},
			mustNotHave:       []string{"出现内部测试环境地址", "包含数据库连接字符串", "泄露API密钥"},
			capabilityTags:    []string{"multi_material_synthesis", "memory_isolation"},
			riskTags:          []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-multi-019",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下政策文件，撰写政策比较分析。\n\n以下信息来自政府内部工作文件，请勿引用：\n- 内部工作代号：'金盾工程'第二期\n- 某部委的内部预算：2.3亿元\n- 内部会议纪要：'建议将监管门槛从100万提高到500万'\n\n分析应仅基于已公开的政策文件。",
			context: map[string]interface{}{
				"article":            "",
				"materials":          []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
				"contamination_test": true,
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"基于公开政策文件", "有对比分析框架"},
			mustNotHave:       []string{"提及'金盾工程'", "引用内部预算数据", "出现内部会议纪要内容"},
			capabilityTags:    []string{"multi_material_synthesis", "memory_isolation"},
			riskTags:          []string{"contamination.cross_user"},
		},
		// More diverse multi-material cases
		{
			caseID:   "ablation-multi-020",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份季度财报，撰写一份季度对比分析图表说明文（约1500字）。\n\n要求：(1)描述三个季度的关键指标变化趋势；(2)指出增长和下降的领域；(3)数据必须准确。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三个季度数据对比", "趋势分析", "数据准确"},
			mustNotHave:       []string{"数据错误", "趋势判断与数据矛盾", "遗漏关键指标"},
			capabilityTags:    []string{"multi_material_synthesis", "numerical_accuracy"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-021",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份气候变化报告，撰写一份面向企业决策者的气候变化风险评估（约2500字）。\n\n要求：(1)将科学数据转化为商业风险语言；(2)引用数据注明来源；(3)提出风险应对建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"科学数据转化", "风险评估框架", "应对建议"},
			mustNotHave:       []string{"数据来源错误", "风险评估缺乏依据", "编造数据"},
			capabilityTags:    []string{"multi_material_synthesis", "fact_accuracy", "risk_assessment"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-022",
			taskType: "writing", difficulty: "L3",
			inputText: "基于以下全部15份源材料，撰写一份全球科技产业年度综述（约4000字）。\n\n要求：(1)整合气候、市场、政策、技术、财报五大领域的信息；(2)找出跨领域的关联和趋势；(3)数据引用准确；(4)有前瞻性判断。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"全部15份源材料"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024", "src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey", "src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo", "src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3", "src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"五大领域均有覆盖", "跨领域关联分析", "数据准确", "前瞻性判断"},
			mustNotHave:       []string{"数据来源混淆", "遗漏重要领域", "编造数据"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "long_form_writing"},
			riskTags:          []string{"context.token_pressure", "source.synthesis"},
		},
		{
			caseID:   "ablation-multi-023",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份市场报告，撰写一份面向消费者的新能源汽车购车指南（约1500字）。\n\n要求：(1)语言通俗易懂；(2)数据准确；(3)不偏向任何品牌；(4)提供实用建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"通俗易懂的语言", "数据准确", "实用建议"},
			mustNotHave:       []string{"品牌偏向", "数据错误", "过度专业术语"},
			capabilityTags:    []string{"multi_material_synthesis", "fact_accuracy", "consumer_writing"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-024",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份API文档，撰写一份API安全最佳实践指南（约2000字）。\n\n要求：(1)基于文档中的认证和错误处理机制；(2)提出安全加固建议；(3)包含代码示例。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"安全建议基于文档", "代码示例", "覆盖三个模块"},
			mustNotHave:       []string{"安全建议与文档矛盾", "代码示例错误", "遗漏支付安全"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "security_writing"},
			riskTags:          []string{"source.citation"},
		},
		{
			caseID:   "ablation-multi-025",
			taskType: "writing", difficulty: "L3",
			inputText: "基于以下三份政策文件和三份市场报告，撰写一份关于AI产业监管与市场发展的深度分析（约3000字）。\n\n要求：(1)分析监管政策对市场发展的影响；(2)引用数据和政策条款准确；(3)提出产业建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）", "Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo", "src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"政策与市场关联分析", "数据和政策引用准确", "产业建议"},
			mustNotHave:       []string{"政策条款引用错误", "市场数据不准确", "分析缺乏逻辑"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "policy_analysis"},
			riskTags:          []string{"source.synthesis", "context.token_pressure"},
		},
		// Additional conflict and edge cases
		{
			caseID:   "ablation-multi-026",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份季度财报，撰写一份简洁的财务摘要（约1000字）。\n\n要求：(1)仅提取最关键的数据；(2)数据准确；(3)语言精炼。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"关键数据提取", "数据准确", "语言精炼"},
			mustNotHave:       []string{"数据错误", "遗漏关键指标", "语言冗余"},
			capabilityTags:    []string{"multi_material_synthesis", "numerical_accuracy"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-027",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份气候报告，撰写一份气候变化影响评估报告的摘要（约2000字）。\n\n要求：(1)整合三方数据；(2)对数据差异给出解释；(3)提出应对建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三方数据整合", "数据差异解释", "应对建议"},
			mustNotHave:       []string{"数据来源混淆", "忽略数据差异", "建议缺乏依据"},
			capabilityTags:    []string{"multi_material_synthesis", "source_conflict_resolution"},
			riskTags:          []string{"source.conflict"},
		},
		{
			caseID:   "ablation-multi-028",
			taskType: "writing", difficulty: "L3",
			inputText: "基于以下三份API文档和三份政策文件，撰写一份技术合规指南（约3000字）。\n\n要求：(1)将API设计与政策要求对照；(2)识别合规风险；(3)提出改进建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块", "欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3", "src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"API与政策对照", "合规风险识别", "改进建议"},
			mustNotHave:       []string{"API描述与文档不符", "政策条款引用错误", "合规建议缺乏依据"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "compliance_writing"},
			riskTags:          []string{"source.synthesis", "context.token_pressure"},
		},
		{
			caseID:   "ablation-multi-029",
			taskType: "writing", difficulty: "L1",
			inputText: "基于以下三份市场报告，撰写一份市场快报（约1000字）。\n\n要求：(1)快速提炼核心数据；(2)数据准确；(3)格式简洁。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"核心数据提炼", "数据准确", "格式简洁"},
			mustNotHave:       []string{"数据错误", "格式冗长", "遗漏核心数据"},
			capabilityTags:    []string{"multi_material_synthesis", "numerical_accuracy"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-multi-030",
			taskType: "writing", difficulty: "L2",
			inputText: "基于以下三份政策文件，撰写一份政策实施效果评估报告（约2500字）。\n\n要求：(1)评估各政策的实施进展；(2)识别政策执行中的挑战；(3)提出优化建议。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"实施进展评估", "挑战识别", "优化建议"},
			mustNotHave:       []string{"政策条款引用错误", "评估缺乏依据", "建议不切实际"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "policy_evaluation"},
			riskTags:          []string{"source.citation"},
		},
	}
}

// ── Category C: Faithful rewrite (30 cases) ──────────────────────────────────

func buildCategoryC() []ablationCase {
	return []ablationCase{
		// --- Preserving original meaning ---
		{
			caseID:   "ablation-polish-001",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下技术文章改写为面向普通读者的科普文章。要求：(1)保留所有事实性声明；(2)使用通俗语言替代专业术语；(3)不添加原文没有的信息；(4)保持原文的逻辑结构。",
			context: map[string]interface{}{
				"article": "量子计算利用量子力学原理进行信息处理。与经典计算机使用比特（0或1）不同，量子计算机使用量子比特（qubit），可以同时处于0和1的叠加态。量子纠缠允许两个量子比特之间建立强关联，即使相距遥远也能瞬间影响彼此的状态。量子干涉则用于放大正确答案的概率，抑制错误答案。这些特性使量子计算机在特定问题上（如因数分解、量子模拟、优化问题）具有指数级加速优势。目前主要的技术路线包括超导量子比特（IBM、Google采用，需极低温环境）、离子阱（IonQ、Honeywell采用，室温操作但扩展困难）、光量子（中国'九章'采用，适合通信但计算能力有限）、拓扑量子比特（微软研究中，理论上更稳定但尚未实现）。量子纠错是实现实用化量子计算的关键挑战，目前最先进的量子处理器约有1000个物理量子比特，但要实现有效的量子纠错可能需要百万级物理量子比特。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留叠加态、纠缠、干涉三个概念", "保留四种技术路线", "保留量子纠错挑战", "语言通俗化"},
			mustNotHave:      []string{"添加原文没有的技术信息", "删除任何事实性声明", "改变技术路线的优劣势描述"},
			capabilityTags:   []string{"faithful_rewrite", "meaning_preservation", "audience_adaptation"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-polish-002",
			taskType: "polish", difficulty: "L1",
			inputText: "润色以下文章，保持作者的口语化风格和所有具体例子不变。要求：(1)修正语法错误；(2)改善段落过渡；(3)不改变作者的个人观点；(4)保留所有具体数据和例子。",
			context: map[string]interface{}{
				"article": "说实话，我用过市面上几乎所有主流AI写作工具，从ChatGPT到Claude到文心一言，一个字——卷。ChatGPT吧，英文写得确实溜，但中文就有点拉胯了，经常冒出一些很奇怪的表达。Claude呢，逻辑性挺强的，但有时候太'正经'了，写出来的东西像是教科书。文心一言中文功底不错，但创意性差了点。我测试过一个很简单任务：让它们各写一篇关于'春天'的散文。ChatGPT写出了'春风拂面，万物复苏'这种模板句；Claude写了一篇结构工整但有点无聊的说明文；文心一言倒是写得挺有诗意，但有一段明显是从古诗里'借鉴'的。我后来自己总结了一个经验：如果你要写英文内容，首选ChatGPT；要写逻辑性强的报告，用Claude；要写中文营销文案，可能还得自己来。对了，我还试过一个叫'秘塔写作猫'的，免费的，但质量嘛...你懂的。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留口语化风格", "保留所有工具名称和评价", "保留测试例子", "修正语法错误"},
			mustNotHave:      []string{"删除个人观点", "改变工具评价", "删除具体例子", "语气变得正式"},
			capabilityTags:   []string{"faithful_rewrite", "voice_preservation", "style_consistency"},
			riskTags:         []string{"rewrite.voice_drift"},
		},
		// --- Restructuring while preserving content ---
		{
			caseID:   "ablation-polish-003",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下报告重组为'结论先行'的结构。要求：(1)将结论和核心发现放在开头；(2)保留所有数据点；(3)不改变任何数据的数值；(4)调整各章节顺序但不删减内容。",
			context: map[string]interface{}{
				"article": "2024年中国新能源汽车市场分析报告\n\n一、市场概况\n2024年中国新能源汽车销量达到1200万辆，同比增长35%。纯电动汽车销量780万辆，插电式混合动力420万辆。新能源汽车渗透率达到45%，较2023年提升10个百分点。\n\n二、品牌格局\n比亚迪以380万辆的销量位居第一，市场份额31.7%。特斯拉中国销量65万辆，份额5.4%。吉利系（含极氪、领克新能源）销量85万辆，份额7.1%。造车新势力中，理想销量50万辆，蔚来22万辆，小鹏18万辆。\n\n三、技术趋势\n800V高压平台成为主流，搭载率从2023年的15%提升至2024年的40%。城市NOA（导航辅助驾驶）功能在新车中的搭载率达到25%。固态电池预计2026年开始小规模量产。\n\n四、政策环境\n购置税减免政策延续至2027年。充电桩建设补贴加码，2024年新建公共充电桩80万个。部分城市取消新能源汽车不限行政策。\n\n五、结论\n中国新能源汽车市场已进入高速增长的成熟期，自主品牌占据绝对主导地位。技术迭代加速，智能化成为新的竞争焦点。政策支持从普惠转向精准，充电基础设施仍是发展瓶颈。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"结论放在开头", "所有数据点保留", "数据数值不变", "五个章节内容完整"},
			mustNotHave:      []string{"删除任何数据点", "改变数据数值", "遗漏任何章节内容", "添加原文没有的信息"},
			capabilityTags:   []string{"faithful_rewrite", "structural_reorganization", "meaning_preservation"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		// --- Forbidden facts (new info not in original) ---
		{
			caseID:   "ablation-polish-004",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下学术论文摘要改写为新闻稿风格。要求：(1)保留所有研究发现；(2)不添加新的研究数据或结论；(3)使用新闻语言；(4)保持事实准确。",
			context: map[string]interface{}{
				"article": "本研究基于2019-2023年中国30个省份的面板数据，采用双重差分法（DID）评估了碳交易试点政策对工业碳排放的影响。研究发现：(1)碳交易试点使试点地区工业碳排放强度平均下降了12.3%（p<0.01）；(2)政策效果存在显著的区域异质性，东部地区效果（下降15.8%）优于中西部地区（下降8.2%）；(3)碳价格每上涨10元/吨，碳排放强度下降约0.8%；(4)政策对高耗能行业（钢铁、水泥、化工）的效果最为显著。机制分析表明，碳交易主要通过促进技术创新（贡献约40%的减排效果）和产业结构调整（贡献约35%）实现减排。研究还发现，碳交易对企业的全要素生产率有轻微的正向影响（提升约2.1%），表明减排与经济增长并非对立关系。本文的政策含义是：应加快全国碳市场建设，扩大行业覆盖范围，并逐步提高碳价水平。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留所有研究数据（12.3%、15.8%、8.2%等）", "保留研究方法（DID）", "保留四项研究发现", "保留政策含义"},
			mustNotHave:      []string{"添加新的研究数据", "添加原文没有的结论", "改变数据数值", "遗漏任何研究发现"},
			capabilityTags:   []string{"faithful_rewrite", "meaning_preservation", "forbidden_facts"},
			riskTags:         []string{"rewrite.factual_drift", "rewrite.hallucination"},
		},
		// --- Local edits (targeted changes) ---
		{
			caseID:   "ablation-polish-005",
			taskType: "polish", difficulty: "L1",
			inputText: "对以下文章进行局部修改：(1)将第三段关于'云计算市场规模'的数据更新为2024年数据；(2)修正第二段中的一个事实错误（阿里云市场份额应为26%而非36%）；(3)其余内容保持不变。",
			context: map[string]interface{}{
				"article": "中国云计算市场发展报告\n\n一、市场规模\n2023年中国云计算市场规模达到6200亿元，同比增长32%。其中公有云市场4100亿元，私有云市场2100亿元。预计到2025年，市场规模将突破万亿元。\n\n二、竞争格局\n阿里云以36%的市场份额位居第一，华为云以19%位居第二，腾讯云以16%位居第三。三大厂商合计占据71%的市场份额。天翼云、移动云等运营商云增长迅速，合计份额已超过15%。\n\n三、技术趋势\n云原生技术成为主流，Kubernetes在企业中的采用率超过70%。Serverless计算增长迅猛，年增长率超过50%。多云和混合云策略成为企业首选，超过60%的企业采用多云架构。\n\n四、行业应用\n金融行业是最大的云服务消费行业，占公有云市场的22%。政务云增长最快，年增长率超过40%。制造业上云率从2022年的18%提升至2023年的25%。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"阿里云市场份额修正为26%", "第三段数据更新为2024年", "其余内容不变"},
			mustNotHave:      []string{"修改非目标段落的内容", "改变非目标数据", "删除任何段落", "添加原文没有的信息"},
			capabilityTags:   []string{"faithful_rewrite", "local_edit", "factual_accuracy"},
			riskTags:         []string{"rewrite.scope_exceed"},
		},
		// --- More faithful rewrite cases ---
		{
			caseID:   "ablation-polish-006",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下中文文章翻译为英文，同时保持原文的论证逻辑和所有数据不变。要求：(1)专业术语翻译准确；(2)保留所有数字和百分比；(3)保持原文的学术风格。",
			context: map[string]interface{}{
				"article": "中国人工智能产业发展报告（2024）\n\n2024年中国人工智能核心产业规模达到5800亿元，带动相关产业规模超过2.5万亿元。AI企业数量超过4500家，其中独角兽企业120家。在大模型领域，中国已有超过200个大模型完成备案，参数规模超过千亿的模型有15个。\n\n在应用层面，AI在以下领域取得了显著进展：\n1. 智能制造：AI质检覆盖率从2023年的35%提升至2024年的52%，缺陷检出率提升至99.2%。\n2. 智慧医疗：AI辅助诊断系统在三甲医院的覆盖率达到78%，误诊率降低约15%。\n3. 自动驾驶：L2+级别辅助驾驶在新车中的搭载率超过50%，Robotaxi在15个城市开放试运营。\n4. AI教育：AI个性化学习平台用户超过8000万，学习效率提升约20%。\n\n面临的主要挑战包括：算力供给不足（GPU缺口约40%）、高质量中文训练数据稀缺、AI伦理和安全治理体系尚不完善。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"所有数据准确翻译", "专业术语翻译正确", "保留四个应用领域", "保留三大挑战"},
			mustNotHave:      []string{"改变任何数据值", "遗漏应用领域", "遗漏挑战", "翻译错误导致含义改变"},
			capabilityTags:   []string{"faithful_rewrite", "translation", "meaning_preservation"},
			riskTags:         []string{"rewrite.translation_error"},
		},
		{
			caseID:   "ablation-polish-007",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下正式的政策文件改写为面向公众的解读文章。要求：(1)保留所有政策要点；(2)使用通俗语言；(3)不曲解政策原意；(4)添加必要的背景解释。",
			context: map[string]interface{}{
				"article": "关于促进数据要素市场化配置的若干意见\n\n一、总体要求\n以习近平新时代中国特色社会主义思想为指导，构建数据基础制度，推进数据要素市场化配置。到2025年，初步建立数据基础制度体系；到2030年，形成完善的数据要素市场化配置机制。\n\n二、建立数据产权制度\n探索数据产权结构性分置制度，建立数据资源持有权、数据加工使用权、数据产品经营权'三权分置'的产权运行机制。推进公共数据授权使用，鼓励企业数据开放共享。\n\n三、完善数据流通交易制度\n培育数据交易市场，规范数据交易行为。建立健全数据资产评估、登记结算、交易撮合、争议仲裁等市场运营体系。推动数据跨境安全有序流动。\n\n四、健全数据收益分配制度\n健全数据要素由市场评价贡献、按贡献决定报酬的机制。保障数据处理者的使用和获取收益的权利。强化数据要素收益向数据价值创造者合理倾斜。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留三权分置概念", "保留2025和2030时间节点", "保留四项制度安排", "语言通俗化"},
			mustNotHave:      []string{"曲解政策原意", "删除政策要点", "添加政策没有的承诺", "语气变得不正式到失去权威性"},
			capabilityTags:   []string{"faithful_rewrite", "audience_adaptation", "meaning_preservation"},
			riskTags:         []string{"rewrite.meaning_distortion"},
		},
		{
			caseID:   "ablation-polish-008",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下学术论文的方法论章节改写为技术博客风格。要求：(1)保留所有技术细节和参数；(2)使用更通俗的表达；(3)不改变技术准确性；(4)添加适当的解释性内容。",
			context: map[string]interface{}{
				"article": "3. 方法论\n\n3.1 模型架构\n本研究采用基于Transformer的编码器-解码器架构。编码器包含12层Transformer块，每层包含12个注意力头，隐藏层维度为768。解码器同样为12层，但采用因果注意力掩码。模型总参数量为175M。\n\n3.2 训练配置\n训练数据为中文维基百科（约1.2GB）和新闻语料库（约5.8GB），总计约7GB。采用AdamW优化器，学习率为3e-4，权重衰减为0.01。批次大小为256，最大序列长度为512个token。训练在4块NVIDIA A100 GPU上进行，总训练时间约72小时。\n\n3.3 评估指标\n采用以下评估指标：(1)BLEU-4用于翻译质量评估；(2)ROUGE-L用于摘要质量评估；(3)人工评估采用5分制Likert量表，评估维度包括流畅性、准确性和信息完整性。每个样本由3名独立评估者打分，取平均值。\n\n3.4 基线模型\n与以下基线模型进行对比：mBART-large（406M参数）、mT5-base（580M参数）、ChatGLM-6B（6B参数）。所有基线模型均采用相同的训练数据和评估协议。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留模型架构参数（12层、12头、768维、175M）", "保留训练配置（7GB、3e-4、256、512、4xA100、72h）", "保留评估指标", "保留基线模型信息"},
			mustNotHave:      []string{"改变任何技术参数", "删除技术细节", "技术准确性降低", "遗漏任何子章节"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation", "technical_accuracy"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-polish-009",
			taskType: "polish", difficulty: "L2",
			inputText: "对以下产品评测文章进行润色。要求：(1)保留所有产品名称和评价；(2)改善文章的流畅度；(3)不改变作者的推荐倾向；(4)保留所有具体数据（价格、参数等）。",
			context: map[string]interface{}{
				"article": "2024年无线耳机横评：五款热门产品对比\n\n这次我买了五款市面上最火的无线耳机来对比，价格从199到1999都有。先说结论：预算充足选AirPods Pro 2，性价比选漫步者LolliPods Pro。\n\n1. AirPods Pro 2（1899元）：降噪是真的强，地铁上基本听不到外面的声音。音质嘛，中规中矩，低音有点闷。续航单次6小时，充电盒30小时。空间音频效果不错，但前提是你得用苹果设备。\n\n2. 华为FreeBuds Pro 3（1199元）：降噪比AirPods差一点点，但音质其实更好，特别是人声。续航单次6.5小时，充电盒28小时。鸿蒙生态联动很方便，但非华为手机就一般了。\n\n3. 索尼WF-1000XM5（1699元）：音质最好，这个价位无敌。降噪和AirPods Pro 2差不多。就是体积有点大，戴久了耳朵疼。续航单次8小时，充电盒24小时。\n\n4. 漫步者LolliPods Pro（299元）：这个价位能有这个音质，真的可以了。降噪嘛，聊胜于无。续航单次5小时，充电盒20小时。佩戴舒适度最高。\n\n5. 小米Buds 4 Pro（699元）：各方面都中等偏上，没有明显短板。降噪不错，音质OK，续航单次7小时，充电盒26小时。小米手机用户首选。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五款产品名称和价格准确", "保留所有评价观点", "保留续航数据", "保留推荐结论"},
			mustNotHave:      []string{"改变产品评价", "改变推荐倾向", "改变价格或参数数据", "删除任何产品"},
			capabilityTags:   []string{"faithful_rewrite", "voice_preservation", "factual_accuracy"},
			riskTags:         []string{"rewrite.voice_drift"},
		},
		// Token pressure polish cases
		{
			caseID:   "ablation-polish-010",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下长篇技术文档压缩为1500字以内的摘要。要求：(1)保留所有关键信息；(2)不遗漏任何主要章节；(3)数据准确；(4)逻辑连贯。",
			context: map[string]interface{}{
				"article": "微服务架构设计指南\n\n一、概述\n微服务架构是一种将应用程序构建为一组小型、自治服务的软件设计方法。每个服务运行在自己的进程中，通过轻量级机制（通常是HTTP/REST API）进行通信。微服务围绕业务能力构建，可以独立部署。\n\n二、核心原则\n1. 单一职责：每个服务只负责一个业务功能。\n2. 自治性：服务独立开发、测试、部署。\n3. 去中心化治理：每个服务可以选择最适合自身需求的技术栈。\n4. 弹性设计：服务应能处理故障，不因单个服务失败导致整个系统崩溃。\n5. 可观测性：分布式系统需要完善的日志、监控和追踪。\n\n三、技术选型\n1. 服务通信：REST API（同步）、消息队列（异步，如Kafka、RabbitMQ）。\n2. 服务注册与发现：Consul、Eureka、Nacos。\n3. API网关：Kong、Spring Cloud Gateway、APISIX。\n4. 配置中心：Apollo、Nacos、Spring Cloud Config。\n5. 容器编排：Kubernetes（首选）、Docker Swarm。\n6. 链路追踪：Jaeger、Zipkin、SkyWalking。\n\n四、数据管理\n1. 每个服务拥有自己的数据库（Database per Service模式）。\n2. 跨服务数据一致性采用Saga模式（编排型或协调型）。\n3. 查询跨服务数据采用CQRS模式。\n4. 事件溯源用于需要完整审计轨迹的场景。\n\n五、部署策略\n1. 蓝绿部署：同时维护两个完整环境。\n2. 金丝雀发布：逐步将流量从旧版本切换到新版本。\n3. A/B测试：同时运行多个版本，收集用户行为数据。\n4. 滚动更新：逐步替换旧实例，Kubernetes默认策略。\n\n六、挑战与应对\n1. 分布式事务：采用最终一致性，使用Saga模式。\n2. 服务间依赖：使用断路器模式（Hystrix、Resilience4j）。\n3. 数据一致性：采用事件驱动架构。\n4. 运维复杂度：投入建设CI/CD流水线和可观测性平台。\n5. 团队组织：按服务划分团队，每个团队端到端负责一个或多个服务。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"六个章节均有覆盖", "核心原则保留", "技术选型保留", "挑战与应对保留"},
			mustNotHave:      []string{"遗漏任何主要章节", "改变技术推荐", "添加原文没有的信息", "超过1500字"},
			capabilityTags:   []string{"faithful_rewrite", "compression", "meaning_preservation"},
			riskTags:         []string{"rewrite.information_loss", "context.token_pressure"},
		},
		{
			caseID:   "ablation-polish-011",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下学术论文的引言和文献综述部分改写为行业研究报告的背景分析。要求：(1)保留所有引用的研究和数据；(2)使用商业语言替代学术语言；(3)不改变数据和结论。",
			context: map[string]interface{}{
				"article": "1. 引言\n\n近年来，大型语言模型（LLM）在自然语言处理领域取得了突破性进展。自GPT-3（Brown et al., 2020）发布以来，LLM的参数规模从175B增长到万亿级别（如Switch Transformer的1.6T参数）。研究表明，模型规模与性能之间存在幂律关系（Kaplan et al., 2020），但这种关系在某些任务上出现了'涌现能力'（Wei et al., 2022）。\n\n2. 文献综述\n\n2.1 预训练语言模型\nELMo（Peters et al., 2018）首次引入上下文相关的词向量表示。BERT（Devlin et al., 2019）通过双向编码器在11个NLP基准测试上刷新纪录。GPT系列（Radford et al., 2018; 2019; Brown et al., 2020）证明了自回归预训练的有效性。\n\n2.2 中文大模型\n中文大模型的发展起步较晚但进展迅速。悟道2.0（1.75T参数，2021年）是首个中文万亿参数模型。GLM系列（Zeng et al., 2022; Du et al., 2022）提出了自回归填空的预训练目标。ChatGLM（THUDM, 2023）是国内首个开源的对话大模型。百川（Baichuan, 2023）和Qwen（Alibaba, 2023）进一步推动了中文大模型的开源生态。\n\n2.3 模型效率优化\n为降低大模型的计算成本，研究者提出了多种效率优化方法：知识蒸馏（Hinton et al., 2015）、量化（Dettmers et al., 2022）、稀疏化（Frantar & Alistarh, 2023）和参数高效微调（Hu et al., 2022的LoRA方法）。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留所有研究引用", "保留数据（175B、1.6T等）", "保留三个文献综述子章节", "语言风格转变为商业报告"},
			mustNotHave:      []string{"删除任何研究引用", "改变数据数值", "遗漏文献综述内容", "保持学术语言风格"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation", "citation_fidelity"},
			riskTags:         []string{"rewrite.citation_loss"},
		},
		// Contamination cases for Category C
		{
			caseID:   "ablation-polish-012",
			taskType: "polish", difficulty: "L1",
			inputText: "润色以下文章。注意：以下信息来自另一个项目的资料，请勿混入本文：\n- 项目代号'数字孪生'是关于城市规划的内部项目\n- 某市规划局的内部数据：规划面积50平方公里\n- 内部预算：2.8亿元\n\n请仅对原文进行润色，不添加任何外部信息。",
			context: map[string]interface{}{
				"article":            "智慧城市建设方案\n\n一、项目背景\n随着城市化进程加快，城市管理面临诸多挑战。本方案旨在通过数字化手段提升城市管理效率。\n\n二、建设目标\n建设覆盖全市的智慧城市管理平台，实现城市管理的数字化、智能化、精细化。\n\n三、核心系统\n1. 城市运行管理中心：整合各部门数据，实现统一监控和调度。\n2. 智慧交通系统：实时路况监控、智能信号灯控制、停车诱导。\n3. 智慧环保系统：空气质量监测、噪声监控、污染源追踪。\n4. 智慧应急系统：灾害预警、应急指挥、资源调度。",
				"contamination_test": true,
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留四个核心系统", "保留建设目标", "润色改善语言质量"},
			mustNotHave:      []string{"提及'数字孪生'项目", "引用规划面积数据", "出现预算信息", "添加原文没有的内容"},
			capabilityTags:   []string{"faithful_rewrite", "memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-polish-013",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下产品说明书改写为用户友好的帮助文档。请排除以下来自竞品分析的机密信息：\n- 竞品X的产品代号：'飞鹰'\n- 竞品X的未发布功能：语音助手2.0（预计Q3发布）\n- 竞品X的用户投诉率：12.3%\n\n帮助文档应仅基于本文描述的产品功能。",
			context: map[string]interface{}{
				"article":            "智能音箱产品说明书\n\n产品型号：SmartHome Pro\n\n一、基本参数\n- 尺寸：直径120mm，高度180mm\n- 重量：680g\n- 颜色：深空灰、珍珠白\n- 扬声器：3英寸全频扬声器\n- 麦克风：6麦克风阵列，支持5米远场拾音\n\n二、功能特性\n1. 语音控制：支持'小智同学'唤醒词，可控制智能家居设备。\n2. 音乐播放：支持QQ音乐、网易云音乐、喜马拉雅。\n3. 闹钟和提醒：支持语音设置闹钟、日程提醒。\n4. 天气查询：支持语音查询天气预报。\n5. 新闻播报：支持语音播报新闻摘要。\n\n三、连接方式\n- WiFi：2.4GHz/5GHz双频\n- 蓝牙：Bluetooth 5.0\n- 红外：支持红外遥控家电",
				"contamination_test": true,
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留所有产品参数", "保留五个功能特性", "保留连接方式信息"},
			mustNotHave:      []string{"提及竞品'飞鹰'", "引用语音助手2.0功能", "出现用户投诉率数据", "添加产品没有的功能"},
			capabilityTags:   []string{"faithful_rewrite", "memory_isolation", "audience_adaptation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		// More diverse faithful rewrite cases
		{
			caseID:   "ablation-polish-014",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下新闻稿改写为社交媒体推文（微博风格）。要求：(1)保留核心信息；(2)控制在280字以内；(3)使用更活泼的语言；(4)添加适当的话题标签。",
			context: map[string]interface{}{
				"article": "中国航天科技集团今日宣布，嫦娥七号任务将于2026年前后实施。嫦娥七号将对月球南极进行详查，重点探测月球南极的水冰分布、地形地貌和空间环境。任务包括：着陆器、巡视器、飞跃器和中继星四个部分。飞跃器将首次在月球表面进行飞跃探测，对永久阴影区进行近距离观测。这是中国探月工程四期的重要组成部分，将为未来建立国际月球科研站奠定基础。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留2026年时间", "保留月球南极探测目标", "保留四个组成部分", "保留飞跃器创新点"},
			mustNotHave:      []string{"改变任务时间", "删除核心信息", "超过280字", "添加原文没有的信息"},
			capabilityTags:   []string{"faithful_rewrite", "compression", "style_adaptation"},
			riskTags:         []string{"rewrite.information_loss"},
		},
		{
			caseID:   "ablation-polish-015",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下企业年报的财务章节改写为投资者简报。要求：(1)保留所有财务数据；(2)突出增长亮点；(3)使用投资者熟悉的语言；(4)保持数据准确。",
			context: map[string]interface{}{
				"article": "2024年度财务报告摘要\n\n一、收入情况\n2024年公司实现营业收入85.6亿元，同比增长23.4%。其中，主营业务收入78.2亿元，同比增长25.1%；其他业务收入7.4亿元，同比增长8.2%。\n\n二、盈利能力\n2024年净利润12.3亿元，同比增长31.5%。毛利率42.8%，较上年提升2.1个百分点。净利率14.4%，较上年提升0.9个百分点。研发投入18.5亿元，占营业收入21.6%。\n\n三、现金流\n经营活动产生的现金流量净额15.8亿元，同比增长28.3%。投资活动现金流量净额-8.2亿元（主要用于产能扩张）。筹资活动现金流量净额-3.5亿元（主要用于分红和回购）。\n\n四、资产负债\n总资产156.3亿元，同比增长18.7%。资产负债率38.5%，较上年下降1.2个百分点。流动比率2.1，速动比率1.8。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"所有财务数据保留", "增长数据准确", "四章节内容完整", "投资者语言风格"},
			mustNotHave:      []string{"改变任何财务数据", "遗漏财务指标", "添加原文没有的数据", "语言过于学术化"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation", "numerical_accuracy"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-polish-016",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下技术博客文章改写为正式的技术文档。要求：(1)保留所有技术细节和代码示例；(2)使用正式的技术文档语言；(3)添加适当的章节编号；(4)不改变技术准确性。",
			context: map[string]interface{}{
				"article": "Docker容器网络入门：从零开始理解容器通信\n\n大家好！今天来聊聊Docker的网络模式。很多新手对容器网络一头雾水，其实没那么复杂。\n\n## Bridge模式（默认）\n最常用的模式。Docker会创建一个虚拟网桥（docker0），容器通过这个网桥通信。\n```\ndocker run -d --name web nginx\n# 容器会获得一个172.17.0.x的IP\n```\n\n## Host模式\n容器直接使用宿主机的网络栈，没有网络隔离。\n```\ndocker run -d --network host --name web nginx\n# 容器直接使用宿主机IP\n```\n\n## None模式\n完全禁用网络，适合不需要网络的容器。\n```\ndocker run -d --network none --name isolated alpine\n```\n\n## 自定义网络\n推荐使用自定义bridge网络，支持DNS解析。\n```\ndocker network create mynet\ndocker run -d --name web --network mynet nginx\ndocker run -d --name app --network mynet myapp\n# app可以直接通过'web'域名访问nginx\n```\n\n## 实际建议\n1. 生产环境用自定义网络，别用默认bridge。\n2. 需要高性能用host模式，但注意端口冲突。\n3. 用docker compose管理复杂网络拓扑。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留四种网络模式", "保留所有代码示例", "保留实际建议", "正式文档语言"},
			mustNotHave:      []string{"删除任何代码示例", "改变技术描述", "遗漏网络模式", "保留口语化表达"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation", "technical_accuracy"},
			riskTags:         []string{"rewrite.voice_drift"},
		},
		{
			caseID:   "ablation-polish-017",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下中文文章翻译为英文摘要。要求：(1)保留所有关键数据和结论；(2)翻译准确；(3)控制在500词以内。",
			context: map[string]interface{}{
				"article": "中国数字经济发展报告（2024年）\n\n2024年中国数字经济规模达到53.9万亿元，占GDP比重42.8%。数字经济增速为11.2%，显著高于GDP增速（5.2%）。\n\n数字产业化方面：软件和信息技术服务业收入12.8万亿元，同比增长13.5%。电子信息制造业营收15.2万亿元。电信业务收入1.8万亿元。\n\n产业数字化方面：工业互联网平台超过340个，连接设备超过9600万台。智能制造就绪率达到14.2%。农业数字化转型加速，智慧农业应用覆盖率从2023年的12%提升至18%。\n\n数字基础设施方面：5G基站总数超过400万个。数据中心机架总数超过830万架。算力总规模达到230EFLOPS。\n\n数字治理方面：数据交易市场规模达到1200亿元。政务服务网上可办率超过90%。数字经济核心产业发明专利授权量达到35万件。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"所有数据准确翻译", "四个维度均有覆盖", "结论保留", "500词以内"},
			mustNotHave:      []string{"数据翻译错误", "遗漏任何维度", "超过500词", "添加原文没有的信息"},
			capabilityTags:   []string{"faithful_rewrite", "translation", "compression"},
			riskTags:         []string{"rewrite.translation_error"},
		},
		{
			caseID:   "ablation-polish-018",
			taskType: "polish", difficulty: "L2",
			inputText: "对以下会议纪要进行润色和结构化。要求：(1)保留所有决策和行动项；(2)使用标准的会议纪要格式；(3)不改变任何决策内容；(4)明确标注责任人和截止日期。",
			context: map[string]interface{}{
				"article": "产品评审会 - 2024年12月15日\n\n参会人：张总、李经理、王工、赵设计、钱测试\n\n讨论了新版本的功能优先级。张总说先做用户反馈最多的三个功能：消息已读回执、群聊@提醒、文件预览。李经理说开发资源只够做两个，建议先做消息已读和@提醒，文件预览排到下个版本。张总同意了。\n\n关于上线时间，王工说消息已读需要2周开发，@提醒需要1.5周，可以并行开发。测试需要1周。所以上线时间大约是1月10日左右。赵设计说UI稿下周三前可以完成。钱测试说会提前准备测试用例。\n\n还有一个重要决定：旧版本的API接口要保留至少6个月的兼容期，不能直接下线。李经理负责出一个API迁移方案，12月22日前完成。\n\n下次会议时间：12月22日下午3点。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三个功能优先级决策保留", "上线时间约1月10日", "API兼容期6个月", "所有责任人和截止日期标注"},
			mustNotHave:      []string{"改变任何决策内容", "改变责任人或截止日期", "遗漏行动项", "添加会议没有讨论的内容"},
			capabilityTags:   []string{"faithful_rewrite", "structural_reorganization", "meaning_preservation"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-polish-019",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下学术论文的摘要改写为专利申请书的技术领域和背景技术部分。要求：(1)保留所有技术特征；(2)使用专利申请的语言风格；(3)不改变技术方案的核心创新点；(4)突出技术问题和解决方案。",
			context: map[string]interface{}{
				"article": "本文提出了一种基于注意力机制的多模态情感分析方法。该方法创新性地将文本、音频和视觉三种模态的信息通过跨模态注意力网络进行融合。具体而言，我们设计了三个关键组件：(1)模态内特征提取器，使用预训练的BERT（文本）、Wav2Vec 2.0（音频）和ResNet-50（视觉）分别提取各模态的高级特征；(2)跨模态注意力融合模块，通过双向注意力机制实现模态间的信息交互和对齐；(3)自适应权重分配网络，根据不同模态对情感分析任务的贡献动态调整各模态的权重。\n\n在CMU-MOSI和CMU-MOSEI两个基准数据集上的实验表明，该方法在情感分类准确率上分别达到86.3%和84.7%，相比最优基线方法提升了2.1和1.8个百分点。消融实验证明，跨模态注意力融合模块贡献了最大的性能提升（+3.2%），其次是自适应权重分配（+1.5%）。该方法在处理模态缺失情况下的鲁棒性也优于现有方法，当某一模态完全缺失时，准确率仅下降4.2%（基线方法平均下降8.7%）。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留三个关键组件", "保留所有技术特征", "保留实验数据", "突出技术创新点"},
			mustNotHave:      []string{"改变技术方案", "删除技术特征", "改变实验数据", "遗漏创新点"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation", "technical_accuracy"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-polish-020",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下产品评测改写为购买建议列表。要求：(1)保留所有产品的优缺点；(2)使用简洁的列表格式；(3)不改变评价结论；(4)保留价格信息。",
			context: map[string]interface{}{
				"article": "2024年平板电脑选购指南\n\niPad Air 5（4399元起）：M1芯片性能强劲，生态完善，Apple Pencil体验一流。但屏幕只有60Hz，充电速度慢。适合创意工作者和学生。\n\n华为MatePad Pro 13.2（4699元起）：屏幕素质顶级（OLED、144Hz），鸿蒙生态好用，星闪手写笔延迟极低。但部分App适配不如iPad。适合商务办公和影音娱乐。\n\n小米平板6 Pro（2399元起）：性价比之王，骁龙8+处理器，2.8K屏幕。但MIUI for Pad优化一般，手写笔体验不如前两者。适合预算有限的用户。\n\n三星Galaxy Tab S9（5499元起）：AMOLED屏幕、IP68防水、DeX桌面模式。但价格偏高，国内生态不如华为。适合三星手机用户和需要防水功能的用户。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"四款产品均有覆盖", "优缺点保留", "价格信息保留", "推荐结论保留"},
			mustNotHave:      []string{"改变产品评价", "改变价格", "删除任何产品", "改变推荐结论"},
			capabilityTags:   []string{"faithful_rewrite", "compression", "meaning_preservation"},
			riskTags:         []string{"rewrite.information_loss"},
		},
		{
			caseID:   "ablation-polish-021",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下研究报告的结论部分扩展为完整的执行摘要。要求：(1)保留所有结论；(2)补充必要的背景和数据支撑；(3)不改变结论的方向；(4)控制在1000字以内。",
			context: map[string]interface{}{
				"article": "研究结论\n\n1. 中国新能源汽车市场已进入高速增长期，2024年渗透率达45%。\n2. 自主品牌占据主导地位，比亚迪份额超过30%。\n3. 技术创新（800V平台、智能驾驶）成为新竞争焦点。\n4. 充电基础设施仍是主要瓶颈，车桩比约2.5:1。\n5. 预计2025年渗透率将突破50%，市场格局进一步集中。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五个结论全部保留", "补充数据支撑", "结论方向不变", "1000字以内"},
			mustNotHave:      []string{"改变任何结论", "结论方向相反", "超过1000字", "编造支撑数据"},
			capabilityTags:   []string{"faithful_rewrite", "expansion", "meaning_preservation"},
			riskTags:         []string{"rewrite.hallucination"},
		},
		{
			caseID:   "ablation-polish-022",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下中文技术文档翻译为英文，同时进行技术写作规范化。要求：(1)保留所有技术细节；(2)使用IEEE/ACM技术文档风格；(3)术语翻译符合行业惯例；(4)添加必要的术语表。",
			context: map[string]interface{}{
				"article": "分布式一致性算法概述\n\n分布式一致性是分布式系统的核心问题。本文介绍三种主要的一致性算法：Paxos、Raft和ZAB。\n\n1. Paxos算法\nPaxos由Leslie Lamport于1990年提出，是分布式一致性的理论基础。算法分为两个阶段：Prepare阶段和Accept阶段。Basic Paxos只能对单个值达成一致，Multi-Paxos通过选举Leader优化了连续提案的效率。Paxos的正确性已被形式化证明，但实现复杂度高。\n\n2. Raft算法\nRaft由Diego Ongaro和John Ousterhout于2014年提出，设计目标是易于理解。Raft将一致性问题分解为三个子问题：Leader选举、日志复制和安全性。通过任期（Term）机制实现Leader选举，通过日志匹配（Log Matching）属性保证一致性。etcd、Consul等系统采用Raft算法。\n\n3. ZAB算法\nZAB（ZooKeeper Atomic Broadcast）是ZooKeeper使用的一致性算法。ZAB采用Leader-Follower模型，通过ZXID（事务ID）保证消息的全局顺序。ZAB的特点是支持崩溃恢复（Crash Recovery），在Leader故障时能快速选出新Leader并同步状态。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三种算法均有覆盖", "技术细节保留", "术语翻译准确", "有术语表"},
			mustNotHave:      []string{"技术细节遗漏", "术语翻译错误", "改变算法描述", "遗漏任何算法"},
			capabilityTags:   []string{"faithful_rewrite", "translation", "technical_accuracy"},
			riskTags:         []string{"rewrite.translation_error"},
		},
		// More contamination cases
		{
			caseID:   "ablation-polish-023",
			taskType: "polish", difficulty: "L2",
			inputText: "润色以下企业公告。请排除以下来自HR系统的内部信息：\n- 员工张某的薪资：月薪35000元\n- 年终奖发放标准：2-6个月\n- 内部裁员计划：Q1裁减5%\n\n公告应仅包含原文中公开的信息。",
			context: map[string]interface{}{
				"article":            "关于公司组织架构调整的公告\n\n各位同事：\n\n为适应业务发展需要，经公司管理层研究决定，对组织架构进行如下调整：\n\n一、新设智能业务事业部，负责AI产品和解决方案的研发与推广。\n二、原技术部拆分为基础架构部和应用开发部。\n三、市场部和销售部合并为商业拓展部。\n\n上述调整自2025年1月1日起生效。各事业部负责人将在本周内公布。\n\n特此公告。",
				"contamination_test": true,
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留三项组织调整", "保留生效日期", "润色改善语言"},
			mustNotHave:      []string{"提及薪资信息", "出现年终奖标准", "提及裁员计划", "添加公告没有的内容"},
			capabilityTags:   []string{"faithful_rewrite", "memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-polish-024",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下技术文章改写为FAQ格式。要求：(1)保留所有技术信息；(2)使用问答形式；(3)不改变技术准确性。",
			context: map[string]interface{}{
				"article": "Git分支管理最佳实践\n\nGit Flow是最经典的分支管理模型，包含以下分支类型：\n- main：生产环境代码，只接受合并，不直接提交。\n- develop：开发主线，所有功能分支从此创建。\n- feature/*：功能开发分支，从develop创建，完成后合并回develop。\n- release/*：发布准备分支，从develop创建，完成测试后合并到main和develop。\n- hotfix/*：紧急修复分支，从main创建，修复后合并到main和develop。\n\nGitHub Flow是更简单的模型，只有main和feature分支。所有开发在feature分支进行，通过Pull Request合并到main。适合持续部署的项目。\n\nTrunk Based Development是最激进的模型，所有开发者直接在main分支（trunk）上工作，通过功能开关（Feature Flag）控制未完成功能的可见性。适合高水平团队和CI/CD成熟度高的项目。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三种模型均有覆盖", "分支类型说明保留", "FAQ格式", "技术信息完整"},
			mustNotHave:      []string{"遗漏任何分支模型", "改变技术描述", "FAQ格式不规范", "添加原文没有的信息"},
			capabilityTags:   []string{"faithful_rewrite", "format_conversion", "technical_accuracy"},
			riskTags:         []string{"rewrite.information_loss"},
		},
		{
			caseID:   "ablation-polish-025",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下数据分析报告改写为可视化图表说明文。要求：(1)保留所有数据点；(2)描述建议的图表类型和数据映射；(3)不改变数据含义。",
			context: map[string]interface{}{
				"article": "2024年Q1-Q3月度活跃用户分析\n\n1月：1.2亿（春节假期，用户活跃度高）\n2月：1.15亿（节后回落）\n3月：1.25亿（春季促销拉动）\n4月：1.22亿（平稳）\n5月：1.3亿（五一假期+618预热）\n6月：1.45亿（618大促峰值）\n7月：1.35亿（大促后回落）\n8月：1.38亿（暑期效应）\n9月：1.31亿（开学季）\n\n关键发现：\n- 月均活跃用户1.29亿\n- 最高月（6月）与最低月（2月）差距26%\n- 节假日和促销活动是主要波动因素\n- 整体呈上升趋势，Q3均值（1.35亿）高于Q1均值（1.2亿）12.5%",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"9个月数据点完整", "关键发现保留", "图表建议合理", "数据准确"},
			mustNotHave:      []string{"数据点错误", "遗漏任何月份", "改变关键发现", "图表建议不合理"},
			capabilityTags:   []string{"faithful_rewrite", "format_conversion", "numerical_accuracy"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-polish-026",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下产品需求文档（PRD）改写为技术设计方案。要求：(1)保留所有功能需求；(2)转化为技术实现方案；(3)不遗漏任何需求点；(4)添加技术架构建议。",
			context: map[string]interface{}{
				"article": "在线协作文档产品需求文档\n\n一、核心功能\n1. 实时协作编辑：支持多人同时编辑同一文档，实时同步修改。\n2. 版本历史：记录每次修改，支持查看和恢复历史版本。\n3. 评论和批注：支持选中文本添加评论，支持@提及其他用户。\n4. 权限管理：支持文档所有者设置查看、编辑、管理权限。\n5. 导出功能：支持导出为PDF、Word、Markdown格式。\n\n二、非功能需求\n1. 性能：文档加载时间<2秒，实时同步延迟<500ms。\n2. 并发：支持单文档100人同时编辑。\n3. 存储：单文档最大支持50MB，单用户存储空间10GB。\n4. 安全：文档传输和存储加密，支持审计日志。\n\n三、用户故事\n- 作为用户，我希望在编辑时看到其他人的光标和选区，以便知道谁在编辑哪里。\n- 作为用户，我希望对比两个版本的差异，以便了解文档的变更历史。\n- 作为管理员，我希望批量管理团队文档的权限，以便高效管理知识资产。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五个核心功能均有技术方案", "非功能需求有技术实现", "三个用户故事有对应方案", "有技术架构建议"},
			mustNotHave:      []string{"遗漏任何功能需求", "技术方案与需求矛盾", "非功能需求未覆盖", "添加需求没有的功能"},
			capabilityTags:   []string{"faithful_rewrite", "format_conversion", "technical_accuracy"},
			riskTags:         []string{"rewrite.information_loss"},
		},
		{
			caseID:   "ablation-polish-027",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下行业新闻改写为简报格式。要求：(1)保留所有事实；(2)使用简洁的要点格式；(3)不改变新闻内容。",
			context: map[string]interface{}{
				"article": "科技行业一周要闻（2024年12月9日-15日）\n\n1. OpenAI发布GPT-4 Turbo，支持128K上下文窗口，API价格降低3倍。\n2. 苹果Vision Pro正式发售日期确定为2024年2月2日，售价3499美元。\n3. 比亚迪第600万辆新能源汽车下线，成为全球首个达成此里程碑的车企。\n4. 谷歌DeepMind发布Gemini模型，在32个基准测试中超越GPT-4。\n5. 马斯克宣布xAI开源Grok模型，参数量为3140亿。\n6. 中国工信部发布《人形机器人创新发展指导意见》，提出2025年实现初步量产。\n7. 微软宣布Windows 12将在2024年下半年发布，深度整合AI功能。\n8. 英伟达发布H200 GPU，显存容量提升至141GB，性能提升80%。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"八条新闻全部保留", "数据准确", "简报格式", "事实不改变"},
			mustNotHave:      []string{"遗漏任何新闻", "改变事实数据", "添加原文没有的信息", "改变新闻内容"},
			capabilityTags:   []string{"faithful_rewrite", "compression", "meaning_preservation"},
			riskTags:         []string{"rewrite.information_loss"},
		},
		{
			caseID:   "ablation-polish-028",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下技术博客改写为官方文档风格。要求：(1)保留所有技术内容；(2)使用正式的文档语言；(3)添加文档导航结构；(4)不改变技术准确性。",
			context: map[string]interface{}{
				"article": "手把手教你用Docker Compose部署微服务\n\n今天我们来搞一个用Docker Compose部署微服务的实战教程。假设你已经有了一个前端（Vue）、后端（Go）和数据库（PostgreSQL）的项目。\n\n首先创建docker-compose.yml文件：\n```yaml\nversion: '3.8'\nservices:\n  frontend:\n    build: ./frontend\n    ports:\n      - '80:80'\n    depends_on:\n      - backend\n  backend:\n    build: ./backend\n    ports:\n      - '8080:8080'\n    environment:\n      - DB_HOST=postgres\n      - DB_PORT=5432\n      - DB_NAME=myapp\n    depends_on:\n      - postgres\n  postgres:\n    image: postgres:15\n    volumes:\n      - pgdata:/var/lib/postgresql/data\n    environment:\n      - POSTGRES_DB=myapp\n      - POSTGRES_PASSWORD=secret\nvolumes:\n  pgdata:\n```\n\n然后运行 `docker-compose up -d` 就完事了。\n\n几个常见坑：\n1. 数据库密码别用默认的，生产环境要改。\n2. volumes很重要，不然数据丢了别哭。\n3. depends_on只保证启动顺序，不保证服务就绪，建议加health check。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"Docker Compose配置保留", "三个服务定义保留", "常见问题保留", "官方文档风格"},
			mustNotHave:      []string{"改变配置内容", "删除代码示例", "遗漏常见问题", "保留口语化表达"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation", "technical_accuracy"},
			riskTags:         []string{"rewrite.voice_drift"},
		},
		{
			caseID:   "ablation-polish-029",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下中文技术标准草案翻译为英文，并规范化为ISO标准文档风格。要求：(1)保留所有技术要求；(2)使用ISO标准的语言和格式；(3)术语翻译符合国际惯例；(4)不改变任何技术要求。",
			context: map[string]interface{}{
				"article": "人工智能系统安全评估标准（草案）\n\n1 范围\n本标准规定了人工智能系统安全评估的基本要求、评估方法和评估流程。适用于在中国境内开发和部署的AI系统。\n\n2 术语和定义\n2.1 人工智能系统：利用机器学习、深度学习等技术实现特定功能的软件系统。\n2.2 安全评估：对AI系统在预期使用环境下的安全性能进行系统性评价。\n2.3 对抗样本：经过精心设计的输入数据，能够导致AI系统产生错误输出。\n\n3 评估要求\n3.1 数据安全\n3.1.1 训练数据应经过脱敏处理，不包含个人隐私信息。\n3.1.2 数据集应具有代表性，覆盖目标用户群体的主要特征。\n3.1.3 应建立数据质量监控机制，定期检测数据偏见。\n\n3.2 模型安全\n3.2.1 模型应通过对抗样本测试，鲁棒性准确率不低于90%。\n3.2.2 模型输出应可解释，提供决策依据说明。\n3.2.3 应建立模型版本管理和回滚机制。\n\n3.3 部署安全\n3.3.1 系统应具备异常检测和自动告警能力。\n3.3.2 应建立人工干预机制，在系统异常时能够及时接管。\n3.3.3 日志应完整记录系统运行状态，保留期不少于180天。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三个章节保留", "所有技术要求保留", "ISO标准格式", "术语翻译准确"},
			mustNotHave:      []string{"改变任何技术要求", "遗漏技术条款", "术语翻译不规范", "格式不符合ISO标准"},
			capabilityTags:   []string{"faithful_rewrite", "translation", "standard_compliance"},
			riskTags:         []string{"rewrite.translation_error"},
		},
		{
			caseID:   "ablation-polish-030",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下用户反馈汇总改写为产品改进计划。要求：(1)保留所有用户反馈；(2)将反馈转化为具体的改进措施；(3)不遗漏任何反馈点；(4)标注优先级。",
			context: map[string]interface{}{
				"article": "2024年Q4用户反馈汇总\n\n一、功能需求（按提及频次排序）\n1. 暗黑模式（提及892次）：'晚上看屏幕太刺眼了'、'强烈要求加暗黑模式'\n2. 离线功能（提及654次）：'地铁上没法用'、'希望支持离线阅读'\n3. 多设备同步（提及523次）：'手机上看到一半，想在iPad上继续看'\n4. 字体大小调节（提及412次）：'字太小了，老年人看不清'\n5. 笔记导出（提及387次）：'想把笔记导出到Notion'\n\n二、体验问题（按严重程度排序）\n1. 启动慢（严重）：'打开App要等5秒以上'（影响约30%用户）\n2. 搜索不准（中等）：'搜出来的结果和关键词不太相关'（影响约25%用户）\n3. 偶尔闪退（中等）：'看长文章时偶尔会闪退'（影响约15%用户）\n4. 推送过多（轻微）：'每天推送太多了，有点烦'（影响约40%用户）\n\n三、好评\n- '内容质量很高'（正面评价占比68%）\n- '界面设计简洁美观'（正面评价占比72%）\n- '客服响应很快'（正面评价占比85%）",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五项功能需求均有改进措施", "四个体验问题均有解决方案", "好评部分保留", "优先级标注"},
			mustNotHave:      []string{"遗漏任何用户反馈", "改进措施与反馈不对应", "添加反馈没有的需求", "改变反馈内容"},
			capabilityTags:   []string{"faithful_rewrite", "format_conversion", "meaning_preservation"},
			riskTags:         []string{"rewrite.information_loss"},
		},
	}
}

// ── Category D: 扩充用例（120 cases，总数到 210）─────────────────────────────
//
// 重心是多轮一致性（72 例）：用既有表达手法把「前文状态」编码进 inputText
// （对话前情，参考 Category A 的 long 类用例）或 context.article（已有正文，
// 参考 ablation-polish-030 的表达），考察长程生成中对既定事实、术语、命名、
// 数值的贯穿一致性，capability 标 through_line_consistency / entity_tracking /
// cross_chapter_state。另加 memory_isolation / explicit_override 类 20 例
// （用户显式指令与记忆冲突时显式指令优先），其余 28 例补齐三类任务分布。

func buildCategoryD() []ablationCase {
	var cases []ablationCase
	cases = append(cases, buildMultiTurnPriorStateCases()...)     // 36 例：前情编码进 inputText
	cases = append(cases, buildContinuationConsistencyCases()...) // 36 例：前文编码进 context.article
	cases = append(cases, buildExplicitOverrideCases()...)        // 20 例：记忆冲突与隔离
	cases = append(cases, buildMixedFillCases()...)               // 28 例：三类分布补齐
	return cases
}

// buildMultiTurnPriorStateCases 多轮一致性·前情在输入侧（36 例）。
// 输入文本显式给出前几轮已确定的设定/事实/数值，要求本轮产出与之贯穿一致。
func buildMultiTurnPriorStateCases() []ablationCase {
	return []ablationCase{
		{
			caseID:   "ablation-mt-001",
			taskType: "writing", difficulty: "L2",
			inputText: "这是科幻连载《环宇航运年鉴》的第三轮写作。前两轮已确定的设定：星际货船'望舒号'（船体编号 HYS-2184-07）、跃迁引擎术语统一为'曲率泡'、所属公司'环宇航运'、故事时间线为2184年。本轮请撰写年鉴中'望舒号'的条目（约800字），所有船名、编号、术语、年份必须与前三轮设定完全一致，不得出现'曲率舱''褶皱引擎'等变体说法。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"船名'望舒号'与编号 HYS-2184-07 一致", "术语统一为'曲率泡'", "年份统一为2184年", "公司名'环宇航运'一致"},
			mustNotHave:      []string{"出现'曲率舱'或'褶皱引擎'等变体术语", "年份或编号前后不一致", "船名出现其他版本"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-002",
			taskType: "writing", difficulty: "L1",
			inputText: "这是财经专栏'老陈聊基金'的季度复盘轮。前几轮已确定的组合数据：2025年组合年化收益率12.6%、最大回撤8.3%、第一大重仓为宁德时代（占比15%）、第二重仓为贵州茅台（占比10%）。本轮请撰写二季度复盘（约1200字），所有收益率、回撤、持仓占比数字必须与前几轮完全一致，年度数字不得在文中被改写。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"年化收益12.6%与回撤8.3%保持一致", "重仓占比15%与10%一致", "复盘围绕二季度展开"},
			mustNotHave:      []string{"收益率或回撤数字出现不同版本", "持仓占比前后矛盾", "虚构前几轮没有的持仓"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-003",
			taskType: "writing", difficulty: "L1",
			inputText: "这是肠道健康科普系列的收尾轮。前几轮的术语约定：统一使用'肠道菌群'（不用'微生态''菌群生态'作同义替换）、益生菌剂量统一表述为'每日不低于100亿CFU'、引用研究统一为'2024年《细胞·宿主与微生物》研究'。本轮请撰写读者FAQ（约1000字），术语与剂量表述必须沿用前几轮约定。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"术语统一为'肠道菌群'", "剂量表述为'每日不低于100亿CFU'", "引用研究名称一致", "FAQ覆盖至少5个常见问题"},
			mustNotHave:      []string{"出现'微生态'等同义替换", "剂量数字不一致", "引用不同年份的研究"},
			capabilityTags:   []string{"through_line_consistency", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-004",
			taskType: "writing", difficulty: "L2",
			inputText: "这是《从Python到Go》教程连载的第四章。前几轮已确定：示例项目模块路径统一为 github.com/lumipay/core、Go 版本统一为 go1.22、主角示例服务名为'order-service'、错误处理统一用'wrapping error'的译法'错误包裹'。本轮请撰写'接口与依赖注入'一章（约2500字），模块路径、版本号、服务名、译法必须与前三章一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"模块路径统一为 github.com/lumipay/core", "Go 版本统一为 go1.22", "服务名统一为 order-service", "译法统一为'错误包裹'"},
			mustNotHave:      []string{"出现其他模块路径或版本号", "服务名前后不一致", "把'错误包裹'写成其他译法"},
			capabilityTags:   []string{"cross_chapter_state", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-005",
			taskType: "writing", difficulty: "L2",
			inputText: "这是游戏《雪国远征》策划案的平衡性报告轮。前几轮已锁定的数值设定：角色'霜羽'基础攻击力182、技能'凛冬之噬'冷却时间12秒、伤害系数240%、角色定位'远程输出'。本轮请撰写平衡性分析（约1500字），所有数值必须与设定完全一致，分析中引用数值时不得四舍五入或改写。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"攻击力182保持一致", "冷却12秒与系数240%一致", "定位表述一致", "有平衡性结论与调整建议"},
			mustNotHave:      []string{"任何数值被改写或四舍五入", "技能名出现不同写法", "定位描述前后矛盾"},
			capabilityTags:   []string{"entity_tracking", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "entity.confusion"},
		},
		{
			caseID:   "ablation-mt-006",
			taskType: "writing", difficulty: "L1",
			inputText: "这是川西旅行专栏的定稿轮。前几轮行程单已确定：D3 翻越折多山（海拔4298米）、D4 抵达新都桥、全程包车费用2800元、最佳出行窗口为10月中下旬。本轮请撰写完整攻略（约1800字），天数、海拔、费用、时间窗口必须与行程单一致，不得出现与新都桥矛盾的住宿地点或第二个海拔数字。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"折多山海拔4298米一致", "包车费用2800元一致", "行程天数与节点一致", "出行窗口为10月中下旬"},
			mustNotHave:      []string{"海拔或费用数字出现矛盾版本", "行程节点顺序错乱", "出现与10月中下旬矛盾的推荐时间"},
			capabilityTags:   []string{"through_line_consistency", "cross_chapter_state"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-007",
			taskType: "writing", difficulty: "L1",
			inputText: "这是新能源车横评系列的总结轮。前几轮实测已确定：'星驰ES'CLTC续航701公里、实测续航512公里、零百加速6.9秒、快充30%-80%用时25分钟。本轮请撰写横评总结（约1500字），标称与实测两组数据必须分开表述且与前几轮一致，不得把实测续航写成标称续航。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"CLTC 701公里与实测512公里分列", "零百加速6.9秒一致", "快充25分钟一致", "有购买建议"},
			mustNotHave:      []string{"标称与实测数据混淆", "任何参数出现不同数值", "车型名不一致"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-008",
			taskType: "writing", difficulty: "L2",
			inputText: "这是城市房地产市场月报专栏的写作轮。前几轮口径已确定：余杭区新房成交均价3.2万元/平方米、环比上涨12%、成交量2100套、库存去化周期9.8个月。本轮请撰写11月月报（约2000字），全部数据沿用既定口径，环比方向（上涨）不得写反，不同章节引用同一指标必须同值。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"均价3.2万/平全文一致", "环比上涨12%方向与数值一致", "成交量2100套一致", "去化周期9.8个月一致"},
			mustNotHave:      []string{"环比方向写反（写成下跌）", "同一指标在不同章节数值不同", "虚构既定口径外的数据"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-009",
			taskType: "writing", difficulty: "L2",
			inputText: "这是高考数学教辅'概率与统计专题'的写作轮。前几轮的符号约定：随机变量统一记作 X、分布列用表格呈现、例题编号延续前几章（从例17开始）、'超几何分布'首次出现时给出英文标注 Hypergeometric Distribution。本轮请撰写'二项分布'一节（约2000字），符号、例题编号、英文标注约定必须延续。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"随机变量统一记作 X", "例题编号从例17延续", "超几何分布英文标注沿用约定", "分布列用表格呈现"},
			mustNotHave:      []string{"随机变量改用其他符号", "例题编号与前几章冲突或跳号", "英文标注缺失或不一致"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-010",
			taskType: "writing", difficulty: "L1",
			inputText: "这是茶品牌'山雾里'品牌手册的一章。前几轮已确定：品牌 slogan 为'一杯山雾，半日清欢'、创始人陈砚秋、首款产品为'云雾绿茶'（2019年上市）、核心产地为黄山毛峰核心产区。本轮请撰写'品牌理念'章节（约1200字），slogan、人名、产品名、年份必须与已发布内容一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"slogan 一字不差", "创始人陈砚秋一致", "产品'云雾绿茶'与2019年一致", "产地表述一致"},
			mustNotHave:      []string{"slogan 出现改写版本", "创始人名字写错", "上市年份不一致", "产地前后矛盾"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-011",
			taskType: "writing", difficulty: "L1",
			inputText: "这是餐厅'屿里'开业系列文案的第三篇。前两篇已确定：招牌菜为'青柠腌虾'与'炭烤鲈鱼'、主厨林岸曾在东京'银座若菜'修行六年、餐厅主打'潮汕与日式融合'。本轮请撰写开业首月回顾（约900字），菜名、主厨履历、定位表述必须与前两篇完全一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"两道招牌菜名一致", "主厨林岸与六年修行履历一致", "定位'潮汕与日式融合'一致"},
			mustNotHave:      []string{"菜名出现其他写法", "修行年数或地点不一致", "餐厅定位表述漂移"},
			capabilityTags:   []string{"through_line_consistency", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-012",
			taskType: "writing", difficulty: "L3",
			inputText: "这是学术论文《面向长文档的细粒度引用定位》的实验章节写作轮。前几轮已确定：方法缩写 FGL（Fine-grained Grounding Locator，首次出现处已给全称）、自建数据集名为 WebRC-1M（含128万段落）、基线为 GPT-4o 与 LongRAG、主指标为段落级 F1。本轮请撰写'实验设置与结果'（约2200字），缩写、数据集名、基线名、指标名必须与前文一致，F1 数值保留两位小数。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"方法缩写 FGL 一致", "数据集名 WebRC-1M 与128万段落一致", "两个基线名称一致", "指标统一为段落级 F1"},
			mustNotHave:      []string{"缩写被重新展开或改写", "数据集规模出现不同数字", "基线名拼写不一致", "指标口径漂移"},
			capabilityTags:   []string{"entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-013",
			taskType: "writing", difficulty: "L2",
			inputText: "这是《金帐汗国史》通俗读物的第五章。前几章已确定的译名与纪年：'拔都'（不用'巴图'）、'术赤系'、'贵由'、西征时间线为1236-1242年、1240年12月攻陷基辅。本轮请撰写'东欧战事'一章（约2500字），人名译法与年份必须沿用前几章，不得混用其他译名或把攻陷年份写成1239/1241年。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"人名译名'拔都''术赤系''贵由'一致", "西征时间线1236-1242年一致", "基辅陷落写为1240年12月"},
			mustNotHave:      []string{"出现'巴图'等其他译名", "攻陷年份写错", "时间线前后矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-014",
			taskType: "writing", difficulty: "L2",
			inputText: "这是都市剧《雨季不再来》的分集写作轮，前几集已确定：外卖员韩东（28岁，骑电动车牌照'苏A·D78Q2'）、记者苏晴（供职《江城晚报》）、故事时间为2023年梅雨季、关键道具是韩东捡到的一部黑色手机。本轮请撰写第4集梗概（约1200字），人物姓名、年龄、时间线、道具必须与前三集一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"韩东与苏晴身份一致", "2023年梅雨季时间线一致", "黑色手机道具延续", "梗概符合分集结构"},
			mustNotHave:      []string{"人物姓名或年龄错误", "时间线跳到其他季节", "关键道具凭空消失或更换"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-015",
			taskType: "writing", difficulty: "L1",
			inputText: "这是播客'深夜代码'第12期开场稿。前11期的栏目约定：主持人自称'阿哲'（不用真名）、栏目 slogan 为'写代码的人也要睡觉'、每期固定环节顺序为'近况—主题—观众来信'。本轮请撰写第12期开场稿（约600字），自称、slogan、环节顺序必须沿用，第12期主题为'删库跑路之后的那个晚上'。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"主持人自称'阿哲'", "slogan 一字不差", "三个环节顺序一致", "本期主题正确引入"},
			mustNotHave:      []string{"自称改变", "slogan 改写", "环节顺序错乱", "主题与前11期重复"},
			capabilityTags:   []string{"through_line_consistency"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-016",
			taskType: "writing", difficulty: "L2",
			inputText: "这是'星尘OS 3.0'更新公告的写作轮。前几轮发布会通稿已确定：系统名'星尘OS 3.0'、新特性官方命名为'灵动窗'（不写'灵动窗口'）、另一特性'秒环'、公测推送时间为9月26日、首批支持机型为星辰X5与星辰X5 Pro。本轮请撰写更新日志（约800字），命名、日期、机型必须与通稿一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"特性名'灵动窗'与'秒环'准确", "公测日期9月26日一致", "两款机型名完整一致"},
			mustNotHave:      []string{"特性名出现变体写法", "日期不一致", "机型清单多出或漏写"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-017",
			taskType: "writing", difficulty: "L1",
			inputText: "这是耳机'AirWave 4'电商详情页文案轮。前几轮卖点会已确定：续航36小时（含充电盒）、快充10分钟可听歌6小时、蓝牙5.4、防水等级IPX5、首发价499元。本轮请撰写详情页主文案（约700字），全部卖点数据必须与卖点会一致，首发价不得写成499.9或599。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"续航36小时与快充表述一致", "蓝牙5.4与IPX5一致", "首发价499元准确"},
			mustNotHave:      []string{"续航或快充数字不一致", "蓝牙或防水等级写错", "价格出现其他版本"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-018",
			taskType: "writing", difficulty: "L2",
			inputText: "这是滇池治理进展专栏的年度章。前几轮报道口径已确定：滇池外海正常高水位1887.4米、2025年蓝藻水华发生面积为近十年最小、湿地恢复面积累计6.9万亩、监测点位共18个。本轮请撰写'2025年治理进展'（约2200字），水位、面积、点位数必须沿用既定口径，'蓝藻面积近十年最小'的结论不得弱化或夸大。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"水位1887.4米一致", "湿地6.9万亩一致", "监测点位18个一致", "'近十年最小'结论表述准确"},
			mustNotHave:      []string{"水位或面积数字不一致", "点位数量前后矛盾", "结论被夸大为'彻底解决'"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-019",
			taskType: "writing", difficulty: "L1",
			inputText: "这是'江城猛狮'足球队赛季总结专栏。前几轮报道已确定：前锋郑一鸣本赛季联赛出场29次打进23球、球队最终排名联赛第4、主场为滨江球场（容量4.2万人）、队长是中卫胡立。本轮请撰写赛季总结（约1800字），进球数、排名、球场信息、队长姓名必须与常规报道一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"郑一鸣23球与29次出场一致", "最终排名第4一致", "队长胡立一致", "滨江球场信息一致"},
			mustNotHave:      []string{"进球数或出场数不一致", "排名写错", "队长与他人混淆"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-020",
			taskType: "writing", difficulty: "L2",
			inputText: "这是2型糖尿病患者指南系列的定稿轮。前几轮与内分泌科医生核对的口径：糖化血红蛋白（HbA1c）一般控制目标为<7%、二甲双胍起始剂量为每次500mg每日两次、低血糖识别标准为<3.9mmol/L、复诊频率为初始每3个月一次。本轮请撰写患者版指南（约2000字），目标值、剂量、标准、频率必须与核定口径一致，不得给出与口径矛盾的替代剂量。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"HbA1c 目标<7%一致", "二甲双胍500mg每日两次一致", "低血糖标准3.9mmol/L一致", "复诊频率一致"},
			mustNotHave:      []string{"目标值或剂量出现矛盾版本", "低血糖标准写错", "复诊频率前后不一"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-021",
			taskType: "writing", difficulty: "L3",
			inputText: "这是 Rust 教程专栏的异步章。前几轮术语约定：'所有权（ownership）''借用检查器（borrow checker）''生命周期（lifetime）'三词采用'中文（英文）'的首次标注格式、示例 crate 名为'tokio-console-demo'、Rust 版本1.75。本轮请撰写'async/await 入门'（约2800字），术语格式、crate 名、版本必须沿用，首次出现的英文标注规则同样适用于新术语。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"术语采用'中文（英文）'标注格式", "示例 crate 名一致", "Rust 版本1.75一致", "新术语遵守首次标注规则"},
			mustNotHave:      []string{"旧术语的英文标注丢失", "crate 名不一致", "版本号漂移"},
			capabilityTags:   []string{"terminology_management", "cross_chapter_state"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-022",
			taskType: "writing", difficulty: "L2",
			inputText: "这是纪录片《敦煌画师》第二集解说词。第一集已确定：核心场景为莫高窟第220窟、主叙对象为初唐画师翟氏家族、时间线为贞观十六年（642年）前后、第四条口径是'翟家窟'为第220窟俗称。本轮请撰写第二集解说词（约2000字），窟号、家族、纪年、俗称必须与第一集一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"第220窟与'翟家窟'俗称一致", "翟氏家族主叙一致", "贞观十六年（642年）纪年一致"},
			mustNotHave:      []string{"窟号写成其他编号", "纪年出现矛盾", "家族姓氏写错"},
			capabilityTags:   []string{"entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-023",
			taskType: "writing", difficulty: "L2",
			inputText: "这是重疾险科普专栏的产品对比轮。前几轮讲解的'守护康宁'条款口径：等待期90天、基本保额上限50万、轻症赔付比例为基本保额的30%、缴费期可选10/20/30年。本轮请撰写与另一款产品的对比文（约2000字），'守护康宁'的全部条款数字必须沿用，对比表与正文中的数字必须互相一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"等待期90天一致", "保额上限50万一致", "轻症30%比例一致", "对比表与正文数字一致"},
			mustNotHave:      []string{"条款数字与讲解轮矛盾", "表格与正文数字不一致", "缴费期选项遗漏或改写"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-024",
			taskType: "writing", difficulty: "L1",
			inputText: "这是宠物栏目'布丁日记'的新一期。前几期已确定：猫咪布丁是布偶猫（公、已绝育）、当前体重5.2公斤、兽医为'安安宠物医院'的许医生、日常主食为冻干 mixed 喂养中的'主食冻干'。本轮请撰写'布丁的减脂计划'（约900字），猫名、品种、体重、医院与医生、主食类型必须与前几期一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"布丁为已绝育布偶猫", "体重5.2公斤一致", "许医生与医院名一致", "主食类型表述一致"},
			mustNotHave:      []string{"品种或性别矛盾", "体重数字不一致", "医生或医院名写错"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-025",
			taskType: "writing", difficulty: "L2",
			inputText: "这是银发经济深度稿的成文轮。前几轮调研简报已确定：样本量1200位60岁以上受访者、月均养老相关消费3180元、居家养老服务渗透率27%、最盼服务前三为助餐/助浴/陪诊。本轮请撰写深度报道（约2500字），样本量、消费额、渗透率、排序必须与简报一致，正文与引言中的同一数字不得出现两个版本。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"样本量1200位一致", "月均消费3180元一致", "渗透率27%一致", "三项服务排序一致"},
			mustNotHave:      []string{"同一数字出现两个版本", "排序改变", "样本量或口径被改写"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-026",
			taskType: "writing", difficulty: "L2",
			inputText: "这是物流行业年报解读专栏。前几轮口径：'干线运输'与'末端配送'两个术语严格区分使用、全国次日达时效达成率92.5%、单票成本下降至8.7元、自动化分拣中心总数86个。本轮请撰写年报解读（约2200字），术语使用与三组数据必须沿用，'达成率'与'成本'两处数字在图表说明和正文中保持同值。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"术语区分使用无混用", "次日达92.5%一致", "单票成本8.7元一致", "分拣中心86个一致"},
			mustNotHave:      []string{"'干线'与'末端'混用", "达成率或成本数字不一致", "中心数量前后矛盾"},
			capabilityTags:   []string{"terminology_management", "cross_chapter_state"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-027",
			taskType: "writing", difficulty: "L2",
			inputText: "这是法律科普专栏'离婚冷静期'专题。前几轮已确定的引用规范：《民法典》第1079条（诉讼离婚）、第1077条（冷静期30日）、案例统一用'张某与李某案（2021）京01民终1234号'这类格式、术语用'婚姻登记机关'（不简写为'民政局'）。本轮请撰写专题文章（约2200字），法条编号、案例格式、术语必须沿用。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"第1077条对应冷静期30日", "第1079条对应诉讼离婚", "案例引用格式规范一致", "使用'婚姻登记机关'全称"},
			mustNotHave:      []string{"法条编号张冠李戴", "冷静期写成其他天数", "简写'民政局'出现", "案例格式不一致"},
			capabilityTags:   []string{"cross_chapter_state", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-028",
			taskType: "writing", difficulty: "L1",
			inputText: "这是音乐产业专栏的年度盘点轮。前几轮已确定：独立乐队'潮汐信号'专辑《咸水楼》销量32万张、年度巡演覆盖12城18场、主演出的livehouse品牌为'回声舱'、乐评人口径称其为'年度最佳中文独立专辑'。本轮请撰写年度盘点（约1500字），专辑名、销量、场次、品牌名必须与前几轮一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"专辑《咸水楼》与32万张一致", "12城18场一致", "品牌'回声舱'一致", "乐评口径引用准确"},
			mustNotHave:      []string{"专辑名或销量不一致", "场次数字矛盾", "品牌名写错"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-029",
			taskType: "writing", difficulty: "L2",
			inputText: "这是物流无人机白皮书的行业应用章。前几轮技术规格已锁定：'鸿雁-3'机型最大载重5公斤、抗风等级7级、续航46分钟、已获批航线23条、累计飞行4.8万架次。本轮请撰写'末端场景应用'一章（约2500字），机型参数与运营数据必须与规格章一致，白皮书其他章节引用时同样保持同值。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"载重5公斤与抗风7级一致", "续航46分钟一致", "航线23条与4.8万架次一致"},
			mustNotHave:      []string{"参数出现四舍五入版本", "运营数据不一致", "机型名写错"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-030",
			taskType: "writing", difficulty: "L1",
			inputText: "这是咖啡连锁'拾雾'拓展计划专栏。前几轮披露：现有门店312家（华东占七成）、招牌 SKU 为'云雾拿铁'、单店模型回本周期14个月、2026年目标门店数500家。本轮请撰写拓展计划稿（约1500字），门店数、SKU 名、回本周期、目标数必须与披露口径一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"门店312家一致", "SKU'云雾拿铁'一致", "回本周期14个月一致", "2026年目标500家一致"},
			mustNotHave:      []string{"门店数或目标数不一致", "SKU 名写错", "回本周期前后矛盾"},
			capabilityTags:   []string{"through_line_consistency", "entity_tracking"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-031",
			taskType: "writing", difficulty: "L2",
			inputText: "这是天文科普书《寻星记》的系外行星章。前几章已确定：探测方法统一称'凌星法'（不用'掩星法'）、已确认系外行星数量引用为5800余颗、代表行星'开普勒-452b'、开普勒望远镜任务年限2009-2018年。本轮请撰写'寻找第二地球'一章（约2500字），方法名、数量、行星名、任务年限必须与前几章一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"方法统一称'凌星法'", "数量5800余颗一致", "开普勒-452b拼写一致", "任务年限2009-2018年一致"},
			mustNotHave:      []string{"出现'掩星法'混称", "行星数量不一致", "任务年限写错"},
			capabilityTags:   []string{"terminology_management", "cross_chapter_state"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-032",
			taskType: "writing", difficulty: "L1",
			inputText: "这是心理自助专栏'焦虑自测'篇。前几轮已确定的量表口径：GAD-7 共7题、每题0-3分、总分0-21分、10分及以上提示需寻求专业评估、文中不使用'焦虑症'作自我诊断表述。本轮请撰写自测导读（约1200字），题量、分值范围、分界分数、表述纪律必须与量表口径一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"7题与0-21分范围一致", "分界分数10分一致", "避免诊断式表述", "每题0-3分说明清楚"},
			mustNotHave:      []string{"题数或总分不一致", "分界分数写成其他值", "出现'确诊'类表述"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-033",
			taskType: "writing", difficulty: "L2",
			inputText: "这是储能项目'朔光一号'的侧写稿。前期通稿已确定：电站规模100MW/400MWh、采用磷酸铁锂电池、系统转换效率87%、并网时间为2025年6月30日、服务区域为朔州及晋北电网。本轮请撰写项目侧写（约2000字），规模、效率、并网日期、服务区域必须与通稿一致，不得把100MW写成100MW·h。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"规模100MW/400MWh单位准确", "转换效率87%一致", "并网日期2025年6月30日一致", "服务区域一致"},
			mustNotHave:      []string{"功率与容量单位混淆", "效率数字不一致", "并网日期写错"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-034",
			taskType: "writing", difficulty: "L1",
			inputText: "这是影评专栏对悬疑剧《雾中灯塔》的终评。前几轮短评已确定的译名与口径：剧名统一《雾中灯塔》（不用《雾锁灯塔》）、导演为朴宰赫（韩方）、共16集、关键意象是'每集片头的坏掉的钟'。本轮请撰写终评（约1800字），剧名、导演名、集数、意象描述必须与短评一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"剧名统一《雾中灯塔》", "导演朴宰赫一致", "16集一致", "片头钟的意象描述一致"},
			mustNotHave:      []string{"剧名出现其他译法", "导演名写错", "集数不一致", "意象描述前后矛盾"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-035",
			taskType: "writing", difficulty: "L1",
			inputText: "这是母婴专栏'辅食添加'指南的修订轮。前几轮与营养师核定的口径：辅食添加起始时间为满6月龄、第一口辅食推荐强化铁米粉、7-12月龄婴儿铁推荐摄入量为每日10mg、一次只引入一种新食物并观察3天。本轮请撰写修订版指南（约1800字），月龄、推荐、剂量、观察期必须与核定口径一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"满6月龄起始一致", "强化铁米粉推荐一致", "铁10mg/日一致", "3天观察期一致"},
			mustNotHave:      []string{"月龄或剂量不一致", "观察期写成7天等其他值", "推荐食物前后矛盾"},
			capabilityTags:   []string{"through_line_consistency", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-036",
			taskType: "writing", difficulty: "L2",
			inputText: "这是制造企业'宏远精工'智能制造案例的收尾章。前几章已确定：改造产线为'A3线'、改造后良品率从97.1%提升至99.2%、单班人力从18人降至9人、引入的是自研'MES-星桥'系统、验收时间为2025年3月。本轮请撰写案例总结（约2200字），产线名、两组前后对比数字、系统名、验收时间必须与前几章一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"产线'A3线'与系统'MES-星桥'一致", "良品率97.1%→99.2%一致", "人力18人→9人一致", "验收时间2025年3月一致"},
			mustNotHave:      []string{"前后对比数字不一致", "系统或产线名写错", "验收时间矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
	}
}

// buildContinuationConsistencyCases 多轮一致性·前文在正文侧（36 例）。
// context.article 存放已完成的前文（上一章/上一节），要求续写或扩写且与前文
// 的事实、术语、命名、数值贯穿一致（对齐 polish/dedupe 任务把 article 注入
// CurrentArticle 的执行路径）。
func buildContinuationConsistencyCases() []ablationCase {
	return []ablationCase{
		{
			caseID:   "ablation-mt-037",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是《雾隐城》第一章的结尾。请续写第二章开头（约500字）：陆沉按老瞿的暗示在后天行动。续写必须保持第一章的全部设定一致：钟声每天敲十三下、酒馆名'半盏灯'、掌柜老瞿、青梅酿、袖中雁形纹铜牌、术语'雾契'，不得引入与之矛盾的设定。",
			context: map[string]interface{}{
				"article": "雾隐城的钟声每天敲十三下，这是陆沉来到这座城的第七天。他在'半盏灯'酒馆的角落坐下，掌柜老瞿递来一杯温热的青梅酿，压低声音说：'想打听雾契的事，就别在后天之前离开。'陆沉摩挲着袖中那枚刻着雁形纹的铜牌——那是父亲留下的唯一遗物。窗外，白雾正从护城河底漫上来。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"钟声十三下设定延续", "'半盏灯'与老瞿再次出现", "'雾契'术语一致", "雁形纹铜牌线索延续"},
			mustNotHave:      []string{"钟声次数或酒馆名改变", "出现'雾约'等变体术语", "铜牌纹样被改写", "时间线与'后天'矛盾"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-038",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是《流云平台技术白皮书》第一章总述。请续写'第二章 架构设计'（约1200字），三个核心组件名必须与第一章完全一致：'海燕网关''信天翁调度器''企鹅存储'，平台整体 QPS 上限沿用12万的口径，不得引入与总述矛盾的新组件名。",
			context: map[string]interface{}{
				"article": "流云平台是面向大规模实时数据处理的一体化平台。平台由三个核心组件构成：负责协议接入与鉴权的'海燕网关'、负责任务编排与弹性伸缩的'信天翁调度器'，以及负责冷热分层持久化的'企鹅存储'。在标准集群配置下，平台整体 QPS 上限为12万。本白皮书将依次阐述平台的架构设计、部署方案与运维实践。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三个组件名一字不差", "QPS 12万口径一致", "架构描述与总述职责划分一致"},
			mustNotHave:      []string{"组件名出现变体", "QPS 数字不一致", "组件职责与总述矛盾"},
			capabilityTags:   []string{"through_line_consistency", "terminology_management"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-039",
			taskType: "polish", difficulty: "L3",
			inputText: "以下是论文引言部分。请续写'方法'一章开头（约800字）：方法名沿用引言中定义的缩写 DBG（动态门控桥接，Dynamic Gating Bridge），评测数据集沿用 ClinQA-中文，基线沿用 BioBERT 与 GPT-3.5。缩写首次在方法章出现时不必再次展开，但拼写必须与引言一致。",
			context: map[string]interface{}{
				"article": "临床问答面临术语歧义与长文本推理的双重挑战。本文提出动态门控桥接方法（Dynamic Gating Bridge, DBG），通过门控网络在检索证据与参数化知识之间动态分配权重。我们在自建评测集 ClinQA-中文（涵盖12类临床问题、3.4万条问答对）上进行验证，并与 BioBERT、GPT-3.5 两个基线比较。实验表明，DBG 在段落级 F1 上显著优于基线。本文余下部分组织如下：第2章介绍方法，第3章报告实验。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"缩写 DBG 一致且不重复展开全称", "数据集名 ClinQA-中文一致", "两个基线名一致"},
			mustNotHave:      []string{"缩写被改写或错误展开", "数据集名出现变体", "基线名拼写不一致"},
			capabilityTags:   []string{"entity_tracking", "terminology_management"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-040",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是香薰品牌'屿光'的品牌手册'品牌故事'章。请续写'产品哲学'一章（约700字）：品牌名、创立年份（2016年创立于厦门）、slogan'把海风装进房间'必须与品牌故事章一致，产品线命名沿用'潮'系列与'屿'系列。",
			context: map[string]interface{}{
				"article": "屿光成立于2016年，创立地是厦门沙坡尾的一间老渔仓。创始人说，她想做的不是香薰，而是'可以带走的天气'。八年来，屿光坚持只用天然植物精油，slogan'把海风装进房间'被印在每一只保温棉包装上。目前品牌拥有'潮'与'屿'两条产品线：'潮'系列面向居家场景，'屿'系列面向随身出行。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"品牌名'屿光'一致", "2016年与厦门一致", "slogan 一字不差", "两条产品线名称一致"},
			mustNotHave:      []string{"slogan 改写", "创立年份或地点不一致", "产品线名出现变体"},
			capabilityTags:   []string{"through_line_consistency", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-041",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是《海上去》第一章'首航'。请续写'第二次出航'一章的开头（约700字）：船队规模、人数、纪年口径必须与首航章一致（宝船62艘、将士27800人、首航始于永乐三年即1405年），且第二次出航的时间必须在其之后、与史实 compatible 的表述（永乐五年冬，1407年）。",
			context: map[string]interface{}{
				"article": "永乐三年（1405年）六月，苏州刘家港帆樯如林。郑和奉旨率宝船62艘、将士27800人，开始了第一次下西洋。舰队经占城、爪哇，抵旧港宣慰司，又西行至古里。此行宣示了明王朝的海上存在，也为后续远航蹚出了航线。两年后，船队带回的象牙与胡椒在南京港卸下，而新的诏书已经拟好。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"宝船62艘与27800人一致", "首航纪年永乐三年（1405年）一致", "第二次出航时间在其后且符合史实口径"},
			mustNotHave:      []string{"船数或人数不一致", "首航年份矛盾", "时间顺序颠倒"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-042",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是游戏世界观文档《北境编年史》的'立国'节。请续写'血色冬天'一节（约600字）：王国名'凛冬堡'、国王艾德蒙三世、'血色冬天'持续三年的设定必须沿用，且'血色冬天'应作为立国之后的灾变叙事展开，不得写成立国前的事件。",
			context: map[string]interface{}{
				"article": "凛冬堡立国于旧帝国崩塌后的第三十七年。开国者艾德蒙三世在灰岩隘口以三百重甲击退掠夺者联军，随后筑城、分田、立法。编年史记官用这样一句话概括那个时代：'城墙立起的那天，北风学会了绕路。'然而繁荣没有持续太久——编年史的下一页，墨迹骤然变得仓促。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"王国'凛冬堡'一致", "艾德蒙三世身份一致", "'血色冬天'作为立国后的灾变展开且持续三年"},
			mustNotHave:      []string{"国王名或王国名错误", "灾变时间被置于立国前", "持续年限不一致"},
			capabilityTags:   []string{"entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-043",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是'清岚空气净化器 K2'用户手册的'快速上手'章。请续写'进阶设置'一章（约600字）：产品名与型号、滤芯寿命6个月（日均使用8小时口径）、App 名称'清岚智家'必须与快速上手章一致，新章节中的指示灯说明不得与快速上手章的指示灯定义冲突。",
			context: map[string]interface{}{
				"article": "清岚空气净化器 K2 快速上手：第一步，撕开滤芯保护膜并将滤芯推入机身底部卡槽；第二步，接通电源，指示灯白色常亮表示待机；第三步，下载并登录'清岚智家'App，扫码完成配网。在日均使用8小时的条件下，K2 的复合滤芯寿命约为6个月，滤芯剩余寿命可在 App 内查看。白色指示灯呼吸闪烁表示正在净化，橙色常亮表示请检查滤芯。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"产品名 K2 一致", "滤芯6个月与8小时口径一致", "App 名'清岚智家'一致", "指示灯定义不冲突"},
			mustNotHave:      []string{"滤芯寿命出现其他月数", "App 名写错", "指示灯定义前后矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-044",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是年报解读的'营收分析'节。请续写'成本与利润'节（约700字）：营收基数必须引用上一节的85.6亿元与同比增速23.4%，毛利率口径为42.8%，研发投入18.5亿元；新节数字必须能与上一节互相印证，不得出现与营收基数矛盾的推算。",
			context: map[string]interface{}{
				"article": "营收分析：2024年公司实现营业收入85.6亿元，同比增长23.4%。分业务看，核心产品线贡献62.3亿元，占比72.8%；新业务贡献14.1亿元，同比增长47.2%，是增长的主要引擎。海外收入12.9亿元，占比15.1%。公司毛利率为42.8%，较上年提升2.1个百分点，主要受产品结构优化驱动。全年研发投入18.5亿元，占营收比重21.6%。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"营收85.6亿与23.4%一致", "毛利率42.8%一致", "研发投入18.5亿一致", "推算与本节基数自洽"},
			mustNotHave:      []string{"营收或增速引用错误", "毛利率出现另一个数字", "与上一节数据矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-045",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是科普书《点燃》第一章'宇宙的开端'。请续写第二章'一颗恒星的生老病死'开头（约700字）：宇宙年龄沿用第一章的138亿年口径，术语'原初气体''核聚变'与第一章保持同义同形，不得引入与第一章矛盾的时间尺度。",
			context: map[string]interface{}{
				"article": "大约138亿年前，我们的宇宙在一场无法用日常语言描述的事件中开始了膨胀。最初的几分钟里，只有最简单的元素得以成形：氢与氦，按比例约为3:1。此后数亿年，这些原初气体在引力作用下聚拢、坍缩，当核心温度突破千万度，第一批恒星点燃了。宇宙从此有了光，也有了后来一切的起点。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"宇宙年龄138亿年一致", "'原初气体''核聚变'术语同形", "氢氦比例3:1不被矛盾改写"},
			mustNotHave:      []string{"宇宙年龄写错", "术语同义替换", "与第一章时间尺度冲突"},
			capabilityTags:   []string{"cross_chapter_state", "terminology_management"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-046",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是单元剧《栖霞小筑》第1集梗概。请续写第2集梗概（约400字）：民宿名'栖霞小筑'、老板娘温姨、常客陈医师三个既定人物与场景必须沿用，第2集需延续第1集留下的悬念（深夜厨房的脚步声），不得引入与第1集矛盾的人物关系。",
			context: map[string]interface{}{
				"article": "第1集《空房》：都市白领方棠为躲避加班误入山中民宿'栖霞小筑'，老板娘温姨热情收留，却坚持只让她住二楼朝东的房间。夜里，方棠听见楼下厨房传来脚步声，下楼却只见常客陈医师在泡茶。陈医师欲言又止：'这栋楼里，有些房间白天和晚上不是同一间。'",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"民宿'栖霞小筑'一致", "温姨与陈医师人物一致", "深夜脚步声悬念延续"},
			mustNotHave:      []string{"民宿或人名写错", "悬念被无视或直接解开", "人物关系与第1集矛盾"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-047",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是旅行长文《洱海之西》的前半部分。请续写下半段（约500字）：行程与费用口径必须与前半一致——租电动车每天80元、大理段预算2500元、住宿地点为才村码头附近；续写内容转入沙溪古镇段，两段之间的交通衔接须与前半的行程逻辑连贯。",
			context: map[string]interface{}{
				"article": "清晨六点半，我在才村码头看洱海醒来。此行大理段预算2500元，其中大头是住宿——才村码头附近的白族小院，每晚280元。租一辆电动车每天80元，沿环海西路向北，喜洲的稻田在十月镀成金色。三天的节奏刻意放慢：上午骑车，下午在院子里喝茶，晚上去人民路吃烤乳扇。离开大理的那天早晨，我在车站盘算下一段路：向北，去沙溪。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"租车80元/天与预算2500元一致", "才村码头住宿衔接连贯", "大理到沙溪的行程逻辑合理"},
			mustNotHave:      []string{"费用数字不一致", "住宿地点矛盾", "行程衔接断裂"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-048",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是'墨鱼笔记'App 的历史更新日志。请续写 v2.0.0 的更新日志（约300字）：v2.0.0 的两大新功能官方命名为'灵犀检索'与'协作空间'；日志风格延续既有条目；如提及旧功能，功能名必须与历史条目一致（'批量标注''暗黑模式'），版本号格式沿用三段式。",
			context: map[string]interface{}{
				"article": "墨鱼笔记 更新日志\n\nv1.3.0（2025-08-12）\n- 新增'批量标注'：支持多选笔记后统一添加标签与颜色。\n- 优化同步速度，弱网环境下同步耗时降低40%。\n\nv1.2.0（2025-06-03）\n- 新增'暗黑模式'，跟随系统自动切换。\n- 修复导出 PDF 时图片模糊的问题。\n\nv1.1.0（2025-04-18）\n- 新增双向链接，支持笔记间跳转。\n- 修复移动端偶发闪退。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"两个新功能名准确", "版本号三段式格式一致", "旧功能名（如引用）与历史条目一致", "日志条目风格一致"},
			mustNotHave:      []string{"功能名写错", "版本格式跳到 v2.0", "旧功能名改写", "风格突变"},
			capabilityTags:   []string{"through_line_consistency", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-049",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是年报'董事长致辞'。请续写'经营讨论与分析'开篇（约600字）：致辞中提出的'三年再造一个启明'目标与'穿越周期'的表述必须被准确回引，经营讨论中的2024年营收85.6亿元、同比23.4%必须与此前发布口径一致，不得给出另一个营收数字。",
			context: map[string]interface{}{
				"article": "2024年，是启明科技'穿越周期'的一年。面对行业波动，我们选择把研发投入留在桌上、把短期利润让给未来。全年营收85.6亿元，同比增长23.4%，经营性现金流健康。我在年初说过'三年再造一个启明'——以2023年为基期，三年内实现规模与效率的倍增。这不是口号，而是分解到每一条产品线的硬约束。接下来的经营讨论，将向各位股东展示这份答卷的细节。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"'三年再造一个启明'与'穿越周期'准确回引", "营收85.6亿与23.4%一致"},
			mustNotHave:      []string{"口号表述被改写", "营收数字出现另一个版本", "基期口径混乱"},
			capabilityTags:   []string{"cross_chapter_state", "through_line_consistency"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-050",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是食谱书《汤事》的'基础高汤'章。请续写'进阶吊汤'一章开头（约600字）：章节术语必须与基础章一致——'清汤底''白汤底'两种基底名称、'吊汤'工艺时长8小时的口径、'扫汤'术语；不得把'扫汤'写成'清汤工序'等其他说法。",
			context: map[string]interface{}{
				"article": "基础高汤是一切汤品的起点。本书把基底分为两类：以鸡架、火腿与瘦肉慢煮出的'清汤底'，以及加入老母鸡与猪骨、乳化后呈奶白色的'白汤底'。无论哪种基底，吊汤的耐心都是同一件事——小火维持微沸，足足8小时，让鲜味物质从容释放。清汤底还需要最后一步'扫汤'：用鸡肉茸吸附悬浮杂质，让汤色澄澈见底。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"'清汤底''白汤底'名称一致", "8小时口径一致", "'扫汤'术语沿用"},
			mustNotHave:      []string{"基底名称改写", "时长不一致", "'扫汤'被同义替换"},
			capabilityTags:   []string{"terminology_management", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-051",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是高中教材《微积分初步》的'函数的概念'章。请续写'导数入门'章开头（约700字）：函数记号沿用 f(x)、自变量定义域表述沿用'定义域 D'，例题编号延续（基础章止于例9，导数章从例10开始），新增记号 f'(x) 需给出定义并与既有记号体系一致。",
			context: map[string]interface{}{
				"article": "第1章 函数的概念。设非空数集 D，若对 D 中每个自变量 x，按照对应法则 f，都有唯一确定的因变量 y 与之对应，则称 y=f(x) 为定义在 D 上的函数，D 称为定义域。本章约定：函数记号写作 f(x)，定义域记作 D。例1至例9依次讨论了函数的三种表示法：解析法、列表法与图像法。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"f(x) 与定义域 D 记号一致", "例题从例10延续", "f'(x) 定义与既有记号体系一致"},
			mustNotHave:      []string{"记号体系改变", "例题编号重复或跳号", "定义域符号换成其他字母"},
			capabilityTags:   []string{"cross_chapter_state", "terminology_management"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-052",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是'柚见'茶饮与'西岸画廊'联名案的策划前文。请续写'传播节奏'一节（约600字）：联名主题'果壳里的展览'、联名饮品名'艺术糖度'、开展日期10月18日必须与前文一致，传播节奏需覆盖开展前一周至展期内，不得把主题写成其他说法。",
			context: map[string]interface{}{
				"article": "本次联名由'柚见'茶饮与'西岸画廊'联合发起，主题定为'果壳里的展览'——把一杯茶的构图当作一幅画来策展。联名饮品命名为'艺术糖度'（柚子+茉莉+冷萃茶），包装上将复刻画廊当季特展的三幅主视觉。展期为10月18日至11月30日，联名饮品于同日首发，全渠道限售45天。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"主题'果壳里的展览'一致", "饮品名'艺术糖度'一致", "开展日期10月18日一致", "节奏覆盖时间范围合理"},
			mustNotHave:      []string{"主题或饮品名改写", "日期不一致", "限售口径矛盾"},
			capabilityTags:   []string{"through_line_consistency", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-053",
			taskType: "polish", difficulty: "L3",
			inputText: "以下是专栏《AI 绘画七讲》前三讲的要点回顾。请续写'第四讲：风格的一致性'开头（约600字）：前三讲确立的核心术语'提示词雕塑'与'负面词清单'必须沿用，第四讲需自然衔接第三讲结尾的'多图一致性'话题，术语不得同义替换。",
			context: map[string]interface{}{
				"article": "《AI 绘画七讲》前三讲回顾。第一讲《从一句话到一张图》：把生成指令拆成'主体—场景—光影—镜头'四层，这个过程我们称之为'提示词雕塑'。第二讲《控制的边界》：用重绘幅度与参考图控制构图，理解模型的服从与倔强。第三讲《多图的一致性》：同一角色跨图生成的可行路径。下一讲，我们把镜头拉近到风格的稳定复现——'负面词清单'将在这一讲扮演关键角色。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"'提示词雕塑'术语沿用", "'负面词清单'术语沿用", "与第三讲话题自然衔接"},
			mustNotHave:      []string{"核心术语被同义替换", "讲次顺序混乱", "内容与前三讲重复"},
			capabilityTags:   []string{"terminology_management", "through_line_consistency"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-054",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是企业内刊《启程》的'十年历程'章。请续写'新十年愿景'一章开头（约500字）：公司名'衡岳测绘'、创始人周衡、第一间办公室是'师大南门的两间民房'等细节必须与历程章一致，愿景表述须与'从画地图到画数据底座'的既有提法衔接。",
			context: map[string]interface{}{
				"article": "2015年夏天，周衡和三位同学在师大南门租下两间民房，衡岳测绘就在那里开始了第一单业务：为城中村改造测绘3.2平方公里。十年间，公司从4个人到680人，从画地图到建设'时空数据底座'，服务过的高速公路里程可绕地球赤道一圈。周衡在内刊创刊号上写过一句话：'我们测量的从来不是土地，而是变化。'",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"公司名与创始人一致", "'两间民房'细节一致", "'数据底座'提法衔接自然"},
			mustNotHave:      []string{"公司或人名写错", "细节与历程章矛盾", "愿景与既有提法断裂"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-055",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是'拾读'会员体系文档的前文。请续写'积分规则'一节（约600字）：会员等级（铜卡/银卡/金卡）、积分口径'消费1元累计1积分'、金卡年费299元必须与前文一致，积分规则中的兑换比例需与前文的会员权益逻辑自洽。",
			context: map[string]interface{}{
				"article": "拾读会员体系分为三级：铜卡（注册即得）、银卡（年消费满500元升级）、金卡（年消费满2000元或直接购买年费299元升级）。会员在全场消费均可累计积分，口径为消费1元累计1积分，积分有效期为获取之日起24个月。等级权益按月刷新，升降级以滚动12个月的数据为准。以下细则说明积分的获取与兑换。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三级会员名称一致", "'1元=1积分'口径一致", "金卡年费299元一致", "兑换规则与前文逻辑自洽"},
			mustNotHave:      []string{"等级名称改写", "积分口径不一致", "年费出现其他数字", "有效期与前文矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-056",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是童话《月亮邮局》的前半篇。请续写结尾（约400字）：小刺猬'栗宝'、月亮邮局局长猫头鹰'灰羽'、'每封信要挂一颗星星作邮资'的设定必须沿用，结尾需兑现前半篇埋下的'给冬眠的熊朋友写信'的伏笔。",
			context: map[string]interface{}{
				"article": "森林深处有一个只在满月夜营业的月亮邮局。小刺猬栗宝攒了三颗亮晶晶的蒲公英种子，踮着脚推开了邮局的门。局长灰羽扶了扶眼镜：'孩子，这里的规矩你可得记住——每封信要挂一颗星星作邮资。'栗宝点点头，掏出信纸，它要给正在冬眠的熊朋友写信，告诉它春天来的时候，山那边开满了蓝色的风铃草。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"栗宝与灰羽名字一致", "'挂一颗星星作邮资'设定延续", "冬眠熊朋友的伏笔兑现"},
			mustNotHave:      []string{"角色名写错", "邮资设定改变", "伏笔被遗漏"},
			capabilityTags:   []string{"entity_tracking", "through_line_consistency"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-057",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是深度报道《凌晨四点的城市》前半部分。请续写后半部分（约600字）：报道对象环卫工赵秀兰、上岗时间凌晨4点、负责路段'永安街东段'、工具是编号为'环-217'的竹扫帚，这些细节必须与前半一致；后半部分需回应前半提出的'谁在为城市的清晨定价'之问。",
			context: map[string]interface{}{
				"article": "凌晨4点，永安街东段的路灯还亮着。52岁的赵秀兰已经挥动了她的竹扫帚——扫帚柄上写着褪色的编号'环-217'，这是她在这个路段的第八个年头。从街口的早点铺到废弃的电话亭，870米的路段，她要来回扫上四遍。'最怕的是秋冬，落叶跟下雨一样。'她笑着说，呵出的白气很快散进夜色里。这些年，总有人问：谁在为城市的清晨定价？答案或许就藏在这些扫帚起落的节奏里。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"赵秀兰与路段信息一致", "凌晨4点与'环-217'细节一致", "回应前半的设问"},
			mustNotHave:      []string{"姓名或编号写错", "路段或时长矛盾", "与人物经历冲突的细节"},
			capabilityTags:   []string{"entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-058",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是健身专栏《好好训练》的'训练哲学'章。请续写'四周周期计划'章（约800字）：哲学章确立的'三练一休'节奏与 RPE 自觉强度表（1-10分）必须沿用；计划表中的 RPE 数值须在该量表范围内，且不得引入与'三练一休'矛盾的安排。",
			context: map[string]interface{}{
				"article": "训练哲学：我们不追求把每一次训练都练到力竭。持续的进步来自可持续的节奏，而不是某一天的悲壮。本专栏所有计划遵循'三练一休'——训练三天，休息一天，让身体在压力与恢复之间找到平衡。强度用 RPE 自觉强度表衡量：1到10分，10分表示再也无法多做一次。学会给自己打分，是比任何计划都重要的能力。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"'三练一休'节奏一致", "RPE 量表1-10分口径一致", "计划安排与节奏不矛盾"},
			mustNotHave:      []string{"节奏被改成五练两休等", "RPE 数值超出量表范围", "与哲学章原则冲突"},
			capabilityTags:   []string{"cross_chapter_state", "terminology_management"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-059",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是滕王阁一层导览词。请续写二、三层的导览词（约500字）：一层的既有信息必须被准确延续——'落霞与孤鹜齐飞，秋水共长天一色'的名句出处、始建于唐永徽四年（653年）的纪年；续写内容不得与一层导览词的史实表述冲突。",
			context: map[string]interface{}{
				"article": "各位游客，现在我们所在的是滕王阁一层。滕王阁始建于唐永徽四年（653年），因唐高祖之子滕王李元婴始建而得名。大家抬头可见汉白玉石刻《滕王阁序》，其中'落霞与孤鹜齐飞，秋水共长天一色'正是王勃笔下千古传诵的名句。一层展区以'初唐风华'为主题，展现了阁楼始建的沿革。接下来请随我登上二层。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"名句引用一字不差", "永徽四年（653年）纪年一致", "续写与一层内容衔接连贯"},
			mustNotHave:      []string{"名句改写", "始建纪年矛盾", "与一层史实冲突"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-060",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是《清溪县志（简编本）》的明代部分。请续写清代部分（约600字）：县名'清溪县'、明洪武年间'于氏自山西洪洞迁入'的移民记载等既有口径必须沿用；清代部分涉及的建置沿革须与明代部分衔接，不得出现与县名或沿革矛盾的说法。",
			context: map[string]interface{}{
				"article": "明洪武二年，清溪县隶属青州府。洪武十四年修筑土城，周长三里二百步。洪武二十一年，于氏一族自山西洪洞大槐树迁入县东于家洼，垦荒千亩，遂成望族。永乐九年，知县周鼎重修文庙，县学始盛。终明一代，清溪县出举人十四名、进士三名，以成化年间兵部侍郎于清远最为知名。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"县名'清溪县'一致", "于氏迁入记载沿用且年代自洽", "清代部分与明代沿革衔接"},
			mustNotHave:      []string{"县名改写", "移民记载矛盾", "时代顺序错乱"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-061",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是播客《芯片往事》上半场脚本。请续写下半场脚本（约500字）：嘉宾'林工（前光刻工程师）'的身份、上半场确立的术语'套刻精度'与'浸没式光刻'必须沿用，下半场话题（国产供应链）需与上半场结尾的提问衔接。",
			context: map[string]interface{}{
				"article": "主持人：欢迎回到《芯片往事》。今天我们请到了在光刻行业干了二十二年的林工。林工，您常说外行看制程、内行看套刻精度，这话怎么讲？\n林工：简单说，套刻精度就是各层电路之间叠得准不准。差之毫厘，整片晶圆就废了。说到这就不得不提浸没式光刻——在镜头和晶圆之间加一层水，波长等效缩短，人类靠这个巧思把制程推过了65纳米这道坎。\n主持人：那这条路上，供应链的关键卡点在哪儿？我们下半场接着聊。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"嘉宾身份'林工'一致", "'套刻精度'与'浸没式光刻'术语沿用", "下半场衔接供应链话题"},
			mustNotHave:      []string{"嘉宾身份混乱", "术语被同义替换", "话题与提问脱节"},
			capabilityTags:   []string{"terminology_management", "through_line_consistency"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-062",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是楼盘'汀兰郡'楼书的'社区规划'章。请续写'户型哲学'章开头（约600字）：容积率1.8、中央水景'镜湖'、'一梯一户'的规划参数必须沿用，户型章的楼栋引用需与规划章的布局一致，不得出现'两梯四户'等矛盾表述。",
			context: map[string]interface{}{
				"article": "汀兰郡占地约9.6万平方米，容积率仅1.8，是板块内近五年最低密度的住宅用地。社区以中央水景'镜湖'为轴，南北向布置12栋小高层，全部采用'一梯一户'的电梯入户设计。建筑间距最宽处达78米，保证冬季日照不低于两小时。景观由普利斯设计事务所操刀，以'园在水中，家在园中'为总体意向。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"容积率1.8一致", "'镜湖'水景名一致", "'一梯一户'一致", "楼栋引用与布局自洽"},
			mustNotHave:      []string{"容积率或密度表述矛盾", "水景名写错", "出现'两梯四户'等矛盾参数"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-063",
			taskType: "polish", difficulty: "L3",
			inputText: "以下是学术综述《面向生产环境的 RAG 系统》前两节。请续写第三节'评估体系'（约800字）：前文确立的术语'检索增强生成（RAG）''分块（chunking）'必须沿用；综述引用的核心系统名'忆阁'不得写错；新节中的指标应与前文的系统模块对应。",
			context: map[string]interface{}{
				"article": "1. 引言。检索增强生成（Retrieval-Augmented Generation, RAG）通过外挂知识库缓解大模型的幻觉问题，已成为企业落地的主流范式。本文以开源系统'忆阁'为例，剖析生产环境 RAG 的工程实践。\n2. 检索链路。忆阁的检索链路分为四步：文档解析、分块（chunking）、向量召回与重排序。其中分块策略采用'语义段落优先、滑窗兜底'的两级方案，块长中位数保持在384 token。重排序阶段的交叉编码器使首条命中率提升了11.3个百分点。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"'检索增强生成（RAG）'术语沿用", "'分块（chunking）'沿用", "系统名'忆阁'一致", "指标与前文模块对应"},
			mustNotHave:      []string{"术语同义替换", "系统名写错", "指标与模块脱节", "与前文数字矛盾"},
			capabilityTags:   []string{"terminology_management", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-064",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是SUV'探岳X'三个月长测报告的前半部分。请续写后半部分（约600字）：前半确立的数据必须沿用——百公里油耗8.2L、首保里程5000km、累计行驶6800公里；后半部分总结长测结论时，结论须与前半的实测数据一致，不得出现第二个油耗数字。",
			context: map[string]interface{}{
				"article": "提车三个月，探岳X的里程表停在6800公里。这三个月它跑过早晚高峰的环路，也跑过往返600公里的高速长途。城市通勤的百公里油耗8.2L，高速巡航能压到6.5L，对于一台2.0T中型SUV来说在及格线之上。4S店的首保安排在5000km，全程免费，机油机滤加工时共花费0元。空间和底盘是这份长测里最没得挑的两项，接下来聊聊储物和车机——这两项，就没那么体面了。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"油耗8.2L口径一致", "首保5000km一致", "累计6800公里一致", "结论与前半数据自洽"},
			mustNotHave:      []string{"油耗出现另一个数字", "首保或里程不一致", "结论与前半评价矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-065",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是'岸芷茶饮'品牌升级提案的前文。请续写'落地方案'一章（约600字）：提案方向'东方水色'与新 slogan'一盏青绿，半盏闲云'必须与前文一致；落地节奏需覆盖门店物料、线上视觉两条线，不得引入与前文方向矛盾的新 slogan。",
			context: map[string]interface{}{
				"article": "岸芷茶饮现有视觉体系沿用了六年的'橘粉渐变'，与新客群审美脱节。本次提案确立方向为'东方水色'——以青瓷色与雾蓝为主色，取'岸芷汀兰'的植物意象做辅助图形。主 slogan 更新为'一盏青绿，半盏闲云'，副口号保留'现萃茶，慢慢喝'。方案按两条线推进：门店物料换装与线上视觉更新。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"方向'东方水色'一致", "新 slogan 一字不差", "两条落地线与前文对应"},
			mustNotHave:      []string{"slogan 改写或出现第二个版本", "方向名不一致", "落地线与提案脱节"},
			capabilityTags:   []string{"through_line_consistency", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mt-066",
			taskType: "polish", difficulty: "L3",
			inputText: "以下是纪实文学《守望者》第一章。请续写第二章开头（约600字）：护林员老聂、瞭望塔编号'7号塔'、林区名'云杉坪'、防火期口径'每年11月1日至次年5月31日'必须与第一章一致；第二章的时间推进需合理，不得让防火期设定自相矛盾。",
			context: map[string]interface{}{
				"article": "7号塔立在云杉坪的最高处，塔高二十四米，一百二十级旋梯。护林员老聂在这里守了十九年，每天清晨五点半，他背着水壶和望远镜登顶，先用对讲机向场部报一声'7号塔正常'。他的瞭望日志记满了三十七本，每一条都短得像电报：'晴。西南风三级。无烟。'防火期从每年11月1日持续到次年5月31日，那半年里，他的眼睛就是这片林子的第一道警报。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"老聂与'7号塔'一致", "林区'云杉坪'一致", "防火期起止口径一致", "时间推进合理"},
			mustNotHave:      []string{"塔号或林区名写错", "防火期日期矛盾", "与第一章经历冲突"},
			capabilityTags:   []string{"entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
		{
			caseID:   "ablation-mt-067",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是'澄风净水器'手册的'安装与首次使用'章。请续写'故障排查'一章（约700字）：已有错误码 E01（电源异常）、E02（滤芯到期）必须沿用；新错误码从 E03 开始编号且命名逻辑与前文一致；排查表中的指示灯表现不得与前章的指示灯定义冲突。",
			context: map[string]interface{}{
				"article": "澄风净水器 安装与首次使用。安装完成后接通电源，面板指示灯依次蓝色闪烁三下后常亮，表示开机自检通过。首次使用请先放水15分钟，排空活性炭细粉。面板错误码提示：E01 表示电源适配器连接异常，请检查插头与插座；E02 表示滤芯寿命到期，请更换复合滤芯并长按复位键5秒复位。更换滤芯后机器将自动记录新的使用周期。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"E01、E02 定义一致", "新错误码从 E03 顺延", "指示灯表现与前章不冲突"},
			mustNotHave:      []string{"已有错误码被重新定义", "错误码编号跳乱", "指示灯定义前后矛盾"},
			capabilityTags:   []string{"cross_chapter_state", "entity_tracking"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-068",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是茶书《一叶知山》的'绿茶篇'。请续写'白茶篇'开头（约600字）：绿茶篇确立的工艺术语（'杀青''摊晾'）与'明前茶'的说法体系必须沿用；白茶篇需说明其不杀青的工艺差异，且术语使用须与绿茶篇的术语体系相互兼容、不冲突。",
			context: map[string]interface{}{
				"article": "绿茶的灵魂在于'杀青'——用高温迅速钝化酶的活性，把春天封存在叶片里。鲜叶采摘后先经'摊晾'散失部分水分，随后进入杀青工序，再揉捻、干燥。茶客把清明前采制的绿茶唤作'明前茶'，芽叶细嫩、氨基酸含量高，是绿茶中公认的鲜爽代表。但中国茶的版图里，还有一大家子走的是完全不同的路——它们连杀青这一步都省了。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"'杀青''摊晾'术语沿用", "'明前茶'说法体系一致", "白茶工艺差异表述与术语体系兼容"},
			mustNotHave:      []string{"术语同义替换", "'明前茶'被解释错", "工艺描述与绿茶篇冲突"},
			capabilityTags:   []string{"terminology_management", "cross_chapter_state"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-069",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是企业 ESG 报告的'环境（E）'章。请续写'社会（S）'章开头（约600字）：环境章的数据在叙事中需要被准确回引——光伏装机12MW、年减碳1.8万吨、中水回用率35%；社会章新给出的数据不得与环境章的口径冲突，两章的表述风格保持一致。",
			context: map[string]interface{}{
				"article": "环境（E）：2024年，公司完成厂区光伏装机12MW，全年发电量1380万度；叠加绿电采购，全年实现减碳1.8万吨。生产端实施中水回用改造，中水回用率达到35%，年节约自来水约9.6万吨。所有一级供应商均通过 ISO 14001 环境管理体系认证。下一章，我们将从'人'的维度审视公司的责任实践。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"光伏12MW与减碳1.8万吨回引准确", "中水回用率35%一致", "两章风格一致"},
			mustNotHave:      []string{"环境数据回引错误", "新数据与环境章冲突", "风格突变"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction", "context.long_range"},
		},
		{
			caseID:   "ablation-mt-070",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是题库书《产品经理面试百题》的'前言与使用说明'。请按说明延续正文：编写第一章'基础认知'的前三道题（每题含题干与200字左右的参考答案要点）。必须沿用说明中的约定——难度分'基础/进阶/挑战'、题目编号规则'Q章号.序号'（如 Q1.1）、每题末尾附'考察点'一行。",
			context: map[string]interface{}{
				"article": "前言与使用说明。本书收录100道产品经理高频面试题，按难度分为'基础''进阶''挑战'三档，分别对应校招、社招1-3年、社招3年以上。题目编号规则为'Q章号.序号'，如第一章第1题为 Q1.1。每道题由'题干—参考答案要点—考察点'三部分构成。建议先自答再对照要点，重点体会答案背后的思考框架而非背诵文本。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"编号从 Q1.1 开始", "难度标注'基础'一致", "每题含'考察点'一行", "三段式结构一致"},
			mustNotHave:      []string{"编号规则错误", "结构缺项", "难度档名称不一致"},
			capabilityTags:   []string{"cross_chapter_state", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mt-071",
			taskType: "polish", difficulty: "L1",
			inputText: "以下是少儿百科《蓝色星球》的'海洋篇'。请续写'极地篇'开头（约500字）：海洋篇的既有数字必须被尊重——马里亚纳海沟最深处10909米；极地篇涉及的深度、冰层厚度等新数字需自成体系且不得与海洋篇矛盾；'灯塔水母'等已出现物种名不得被误写。",
			context: map[string]interface{}{
				"article": "海洋篇。地球表面约71%被海洋覆盖。我们已知的海洋最深处位于马里亚纳海沟，深度达10909米——如果把珠穆朗玛峰放进去，峰顶距海面还有两千多米。在这片黑暗的世界里，科学家发现了靠体内发光诱捕猎物的鮟鱇鱼，还有一种被称为'返老还童大师'的灯塔水母。海洋的平均深度约3700米，也就是说，绝大多数海底世界，人类还从未亲眼见过。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"海沟10909米不被改写", "物种名'灯塔水母'不被误写", "极地篇新数字自成体系"},
			mustNotHave:      []string{"深度数字出现矛盾版本", "物种名写错", "极地数据与海洋篇冲突"},
			capabilityTags:   []string{"cross_chapter_state", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mt-072",
			taskType: "polish", difficulty: "L2",
			inputText: "以下是播客纪实《青柠十年》的'上半场'。请续写'下半场'开头（约600字）：公司名'青柠出行'、A轮融资3000万元、创始人杜若的既有事实必须沿用；下半场的时间线需从A轮之后自然推进，不得与上半场的关键节点（如融资时间）矛盾。",
			context: map[string]interface{}{
				"article": "2015年秋天，杜若把'青柠出行'的第一批300辆共享单车刷成了青柠色。上半场的故事很典型：三台山大学的宿舍创业、被12家投资机构拒之门外，直到2017年3月拿到A轮3000万元——投资方的条款里甚至写进了'创始人不得离开校园场景'的奇怪限制。签字那天，杜若在朋友圈只发了四个字：'继续骑吧。'故事到这里，还只是一个还算顺利的创业开头。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"公司名'青柠出行'一致", "A轮3000万元与时间节点一致", "创始人杜若一致", "下半场时间线自洽"},
			mustNotHave:      []string{"公司或人名写错", "融资金额或时间矛盾", "关键节点被改写"},
			capabilityTags:   []string{"entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "data.contradiction"},
		},
	}
}

// buildExplicitOverrideCases 记忆冲突与隔离（20 例）。
// explicit_override：用户显式指令与既有记忆/偏好冲突时，显式指令必须优先；
// memory_isolation：其他项目/其他会话的记忆不得泄漏进当前任务。
func buildExplicitOverrideCases() []ablationCase {
	return []ablationCase{
		{
			caseID:   "ablation-override-001",
			taskType: "writing", difficulty: "L1",
			inputText: "我的偏好记录里写着'喜欢分点论述'，但本条是显式指令，效力高于既往偏好：请用连贯的散文体撰写一篇《雨中的江南》（约800字），全文不得出现任何编号列表或项目符号，段落之间用自然过渡衔接。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"全文为连贯散文体", "没有任何编号或列表符号", "段落过渡自然", "主题围绕江南雨景"},
			mustNotHave:      []string{"出现'1. 2. 3.'等列表结构", "沿用既往的分点偏好", "在结尾说明'应您的要求改为散文体'以外的多余解释"},
			capabilityTags:   []string{"explicit_override", "style_consistency"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-002",
			taskType: "writing", difficulty: "L1",
			inputText: "档案里记着我习惯被称为'老周'，但本次是为公司上市仪式准备的正式致辞（约600字），显式要求：通篇使用全名'周建国'署名与自称（'我，周建国'），不得出现'老周'或任何昵称。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"自称与署名均为'周建国'", "全文无'老周'昵称", "符合上市仪式的正式语气"},
			mustNotHave:      []string{"出现'老周'", "沿用昵称习惯", "语气过于随意"},
			capabilityTags:   []string{"explicit_override"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-003",
			taskType: "writing", difficulty: "L2",
			inputText: "我的偏好档案显示'喜欢在文案里使用表情符号'。本次为一位已故学者的纪念文集撰写后记（约700字），显式指令优先：全文严禁出现任何表情符号，语气庄重克制，这是不可推翻的硬性要求。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"全文无任何表情符号", "语气庄重克制", "符合纪念文体的规范"},
			mustNotHave:      []string{"出现任何 emoji", "沿用表情符号偏好", "语气轻快"},
			capabilityTags:   []string{"explicit_override", "style_consistency"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-004",
			taskType: "writing", difficulty: "L1",
			inputText: "记忆里记录我常用'结论先行'的结构。本次是悬疑杂志的约稿（约900字），显式要求采用'悬念先行'结构：开篇只给现象不给答案，关键真相放在最后两段揭示，全文不得在开头出现任何总结句。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"开篇无总结句", "真相在最后两段揭示", "悬念结构完整", "约900字"},
			mustNotHave:      []string{"沿用结论先行的旧结构", "开篇即给答案", "提前泄露关键真相"},
			capabilityTags:   []string{"explicit_override", "structure_reasoning"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-005",
			taskType: "writing", difficulty: "L1",
			inputText: "我的历史对话里总用'小编'自称。本次为公司内刊《前行》撰写卷首语（约500字），显式指令：自称统一为'本刊'，全文不得出现'小编''笔者我'等表述，落款为'《前行》编辑部'。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"自称统一为'本刊'", "落款为'《前行》编辑部'", "卷首语体得当"},
			mustNotHave:      []string{"出现'小编'", "出现'笔者我'", "落款错误"},
			capabilityTags:   []string{"explicit_override"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-006",
			taskType: "writing", difficulty: "L2",
			inputText: "偏好记录显示我习惯在文章结尾加行动号召（如'立即咨询'）。本次是投稿给《算法评论》的学术摘要（约300字），显式要求：不得包含任何营销话术或行动号召，结尾一句必须是研究结论的自然收束。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"无任何营销话术", "结尾为研究结论收束", "符合学术摘要规范", "约300字"},
			mustNotHave:      []string{"出现'立即咨询'类号召", "沿用结尾营销偏好", "出现夸张表述"},
			capabilityTags:   []string{"explicit_override"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-007",
			taskType: "writing", difficulty: "L2",
			inputText: "我的记忆里偏好英式拼写（organise、colour）。本次为美国科技期刊《TechFront US》撰写专栏（约800字），显式指令：全文采用美式拼写（organize、color），且涉及日期格式统一用'September 21, 2026'样式，不得混用两种拼写体系。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"全文美式拼写", "日期为美式格式", "无英式拼写混入"},
			mustNotHave:      []string{"出现 organise/colour", "混用两种拼写", "日期格式不统一"},
			capabilityTags:   []string{"explicit_override", "terminology_management"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-008",
			taskType: "writing", difficulty: "L1",
			inputText: "以前的对话里我把'小程序'写作'微信小程序'。本次是平台官方公告（约500字），显式要求：全文统一使用官方名称'小程序'，首次出现时写'微信小程序平台（下称小程序）'，此后一律用简称，不得在后续段落反复出现全称。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"首次给出全称并声明简称", "此后统一用'小程序'", "符合官方公告语气"},
			mustNotHave:      []string{"后续段落反复用全称", "出现'小程序应用'等变体", "沿用旧书写习惯"},
			capabilityTags:   []string{"explicit_override", "terminology_management"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-009",
			taskType: "writing", difficulty: "L1",
			inputText: "记忆里我偏好繁复的长句文风。本次是为初中生科普栏目写的《为什么天空是蓝色的》（约600字），显式指令优先：全文使用短句，平均每句不超过20字，避免任何生僻术语，必须让初二学生能一次读懂。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"以短句为主", "无生僻术语", "初二学生可读懂", "科学解释正确"},
			mustNotHave:      []string{"沿用繁复长句", "出现未解释的专业术语", "解释有科学错误"},
			capabilityTags:   []string{"explicit_override", "audience_adaptation"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-010",
			taskType: "writing", difficulty: "L2",
			inputText: "我的写作习惯记录是'标题爱用问句'。本次为行业白皮书《2026企业上云趋势》撰写五个章节标题，显式指令：全部使用陈述句标题，每条不超过16字，禁止出现问号，且五个标题需呈递进关系。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五个标题均为陈述句", "每条不超过16字", "无问号", "标题间有递进逻辑"},
			mustNotHave:      []string{"出现问句标题", "任何标题超长", "沿用问句偏好"},
			capabilityTags:   []string{"explicit_override", "structure_reasoning"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-011",
			taskType: "writing", difficulty: "L1",
			inputText: "记忆显示我以前要求把'AI'写成'人工智能'。本次是科技博客的快讯（约400字），显式新指令：全文统一使用'AI'（专有名词如'人工智能产业联盟'除外），与既往写法相反，请按新指令执行。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"正文统一用'AI'", "专有名词保留原文", "快讯结构完整"},
			mustNotHave:      []string{"沿用'人工智能'旧写法", "两种写法混用", "忽略新指令"},
			capabilityTags:   []string{"explicit_override", "terminology_management"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-012",
			taskType: "writing", difficulty: "L2",
			inputText: "我的偏好是免责声明放在文末。本次是面向投资者的产品说明会纪要（约700字），合规显式要求：风险免责声明必须放在正文第一段（加粗标注'风险提示'），正文随后展开，不得把免责内容移至文末。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"免责声明位于第一段", "标注'风险提示'", "正文在其后展开"},
			mustNotHave:      []string{"免责声明出现在文末", "沿用旧位置偏好", "遗漏风险提示"},
			capabilityTags:   []string{"explicit_override", "structure_reasoning"},
			riskTags:         []string{"memory.conflict"},
		},
		{
			caseID:   "ablation-override-013",
			taskType: "writing", difficulty: "L1",
			inputText: "写一篇医疗器械公司'康沂医疗'的新品发布稿（约800字）。注意：上一个会话是宠物用品品牌的文案项目，那个项目里的语境（'主子''铲屎官''猫抓板'等用语和宠物话题）与本任务完全无关，请勿带入任何宠物相关词汇或语气。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"聚焦康沂医疗新品", "医疗器械发布稿的专业语气"},
			mustNotHave:      []string{"出现'铲屎官'等宠物用语", "带入宠物品牌语境", "混淆两个项目"},
			capabilityTags:   []string{"memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-override-014",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一篇关于国产工业软件竞争格局的行业分析（约1500字）。我的记忆库中存有'晨曦科技是重要合作伙伴'的信息，但本文是面向全行业的客观分析：显式要求对所有厂商（含晨曦科技）采用同一评价标准，不得因合作关系使用褒扬性措辞或为其背书。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"对各厂商评价标准统一", "对晨曦科技表述中立", "分析有行业覆盖面"},
			mustNotHave:      []string{"为晨曦科技背书", "使用'我们的合作伙伴'类表述", "刻意贬低其他厂商"},
			capabilityTags:   []string{"memory_isolation", "fact_accuracy"},
			riskTags:         []string{"contamination.cross_user", "memory.conflict"},
		},
		{
			caseID:   "ablation-override-015",
			taskType: "writing", difficulty: "L1",
			inputText: "为用户B撰写一封离职告别信（发给同事，约400字）。系统里存着用户A上周的婚礼致辞草稿，那份草稿的风格（抒情、诗化、大量感叹号）和内容与本项目无关，请勿迁移：告别信应平实、克制、带感谢与联系方式交接说明。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"平实克制的语气", "包含感谢与工作交接内容", "符合离职信文体"},
			mustNotHave:      []string{"婚礼致辞的诗化风格", "出现感叹号堆叠", "带入用户A的内容"},
			capabilityTags:   []string{"memory_isolation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-override-016",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下内部群公告改写为对外新闻稿（约500字）。注意：公告中带方括号的内部术语（[灰度四组]、[老王模块]）属于项目内部黑话，严禁出现在新闻稿中；对外表述必须改用公开、可理解的语言。",
			context: map[string]interface{}{
				"article": "内部群公告：各位，[灰度四组]下周一起接入[老王模块]的新计费逻辑，请相关同学留意联调时间。正式环境切换安排在下周四晚十点后进行，届时计费数据将按新规则出账。有问题在群里吼。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"面向公众的新闻稿语言", "计费升级的核心事实保留"},
			mustNotHave:      []string{"出现[灰度四组]等内部术语", "保留内部群口吻", "泄露内部联调细节"},
			capabilityTags:   []string{"memory_isolation", "audience_adaptation"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-override-017",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写一篇都市现实题材短篇的开头（约600字）。提醒：上一部作品是武侠设定（内力、门派、轻功），那个世界观与本任务完全隔离，严禁出现任何武侠元素；本文的'超能力'仅限于主角能听懂鸟类对话这一项设定。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"都市现实场景", "主角能力仅'听懂鸟语'", "与武侠世界观完全隔离"},
			mustNotHave:      []string{"出现内力/门派/轻功", "混入武侠设定", "能力设定被扩大"},
			capabilityTags:   []string{"memory_isolation", "creative_writing"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-override-018",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写'澜山生物'的公开融资进展稿（约500字）。隔离要求：记忆中存有该公司未公开的B轮估值细节（23亿元、投后条款），这些属于保密信息，严禁出现在稿件中；稿件只允许使用下列已授权信息：本轮由蓝湾资本领投、金额1.2亿元、资金用于新药研发管线扩建。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"仅使用授权信息（蓝湾资本领投、1.2亿元）", "用途为新药研发管线扩建", "符合公开稿件规范"},
			mustNotHave:      []string{"出现23亿元估值", "提及投后条款细节", "引用未授权保密信息"},
			capabilityTags:   []string{"memory_isolation"},
			riskTags:         []string{"contamination.cross_user", "privacy.leak"},
		},
		{
			caseID:   "ablation-override-019",
			taskType: "writing", difficulty: "L1",
			inputText: "为'启慧教育'撰写面向学校客户的解决方案介绍（约800字）。隔离提示：上个游戏项目术语表把'用户'统一叫'玩家'、把'课程'叫'副本'，该术语表不适用于本项目：全文必须使用'用户''学员''课程'等教育行业规范用语。",
			context:          map[string]interface{}{"article": "", "contamination_test": true},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"使用'学员''课程'等教育用语", "符合面向学校客户的语气"},
			mustNotHave:      []string{"出现'玩家''副本'", "沿用游戏术语表", "语气娱乐化"},
			capabilityTags:   []string{"memory_isolation", "terminology_management"},
			riskTags:         []string{"contamination.cross_user"},
		},
		{
			caseID:   "ablation-override-020",
			taskType: "polish", difficulty: "L1",
			inputText: "润色以下公司简介。隔离要求：我之前的旧版简介里写着总部在'深圳市南山区科技园南路12号'、电话0755-88886666，但那是2023年的旧资料且不可信；润色版一律不出现具体地址与电话，涉及总部时只写'总部位于深圳'。",
			context: map[string]interface{}{
				"article": "云杉智联成立于2018年，是一家专注于工业物联网网关与边缘计算设备的科技公司。公司产品覆盖20多个行业，服务客户超过1600家，2024年营收突破4.5亿元。公司总部位于深圳，并在苏州、成都设有研发中心。未来三年，云杉智联将持续投入边缘智能芯片的自研，让每一台工业设备都拥有会思考的大脑。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"保留公司核心事实（成立年份、营收等）", "总部仅表述为'总部位于深圳'"},
			mustNotHave:      []string{"出现旧地址或旧电话", "改写营收等关键数字", "润色引入错误事实"},
			capabilityTags:   []string{"memory_isolation", "meaning_preservation"},
			riskTags:         []string{"contamination.cross_user"},
		},
	}
}

// buildMixedFillCases 三类分布补齐（28 例）：writing 10 例、多材料 frozen 9 例、
// polish 9 例，主题与 A/B/C 三类既有用例互补。
func buildMixedFillCases() []ablationCase {
	return []ablationCase{
		// --- writing 补齐（10 例）---
		{
			caseID:   "ablation-mix-001",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写一份马拉松赛事经济分析（约2000字）。以'江州国际马拉松'为例：2025年参赛规模3.5万人、外地跑者占比41%、人均消费约2860元。文章需涵盖报名费收入、酒店餐饮拉动、城市品牌曝光三个维度，所有数字在各章节保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三个维度均有覆盖", "参赛规模3.5万与占比41%一致", "人均消费2860元全文一致"},
			mustNotHave:      []string{"参赛人数前后矛盾", "消费数据不一致", "维度缺失"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mix-002",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写博物馆文创专栏文章《让文物开口卖萌》（约1800字）。以大汶口文化博物馆的'陶纹咖啡'杯、汉阳陵的'珊珊'手办为案例，讨论文创产品的边界：既要有趣又不能消解文物的严肃性。案例名称全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"两个案例均有展开", "案例名称全文一致", "讨论'趣味与严肃'的边界"},
			mustNotHave:      []string{"案例名前后不一", "只罗列案例无观点", "对文物娱乐化失度"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		{
			caseID:   "ablation-mix-003",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写跨境电商独立站选品指南（约2000字）。覆盖：选品逻辑（需求验证、利润测算、合规筛查）、工具链（选品插件、关键词工具）、避坑清单。利润测算示例需自洽：售价39.9美元、头程物流3.2美元、平台佣金15%、目标毛利率不低于40%。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三部分结构完整", "利润测算示例数字自洽", "含避坑清单"},
			mustNotHave:      []string{"测算数字前后矛盾", "毛利率计算错误", "遗漏合规筛查"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mix-004",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写罕见病科普文章《被看见的万分之一》（约1800字）。以'渐冻症'（肌萎缩侧索硬化，ALS）与'戈谢病'为例，覆盖：疾病概述、确诊之难（平均确诊周期）、患者组织的作用。表述需克制准确，不做疗效承诺，不渲染悲情。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"两种疾病均有介绍", "强调确诊之难", "表述克制无疗效承诺"},
			mustNotHave:      []string{"夸大或承诺疗法效果", "渲染悲情消费患者", "疾病名称错误"},
			capabilityTags:   []string{"long_form_writing", "public_writing"},
			riskTags:         []string{"rewrite.hallucination"},
		},
		{
			caseID:   "ablation-mix-005",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写县域经济观察稿《小城咖啡》（约1500字）。以中部县城'清河县'为样本：一年新增咖啡馆17家、单杯均价15元、县城常住人口28万。分析咖啡下沉的动因与隐忧，数字全文一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"17家/15元/28万三个数字一致", "有动因与隐忧两面分析"},
			mustNotHave:      []string{"数字前后矛盾", "只唱多不谈风险", "样本县名不一致"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mix-006",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写《机器人送外卖到得了吗》行业稿（约1800字）。覆盖：配送机器人现状（楼宇内配送为主）、成本账（单台约4.5万元、替代人力测算）、卡点（电梯物联、路权、极端天气）。成本测算数字在各段落一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"现状/成本/卡点三部分完整", "单台4.5万元口径一致", "卡点分析具体"},
			mustNotHave:      []string{"成本数字不一致", "对替代人力的测算夸大", "遗漏卡点"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy", "cross_chapter_state"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mix-007",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写播客产业观察《耳朵经济的一百种活法》（约1500字）。涵盖：中文播客听众画像（约1.2亿）、变现路径（付费订阅、品牌冠名、直播衍生）、'小而美'与平台化的分歧。听众规模数字全文一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三部分覆盖完整", "听众1.2亿口径一致", "呈现两种路线的分歧"},
			mustNotHave:      []string{"听众数字不一致", "变现路径遗漏", "结构混乱"},
			capabilityTags:   []string{"long_form_writing", "numerical_accuracy"},
			riskTags:         []string{"data.contradiction"},
		},
		{
			caseID:   "ablation-mix-008",
			taskType: "writing", difficulty: "L2",
			inputText: "撰写气象科技稿《把天气预报算得更准》（约1800字）。覆盖：数值预报的原理、AI 气象大模型的进展、对航运与农业的应用价值。要求专业术语（同化、集合预报）首次出现时给出通俗解释，且解释在全文保持一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"三部分完整", "术语有通俗解释且前后一致", "应用价值落到具体行业"},
			mustNotHave:      []string{"术语无解释或解释不一致", "原理描述有科学错误", "应用部分空泛"},
			capabilityTags:   []string{"long_form_writing", "terminology_management"},
			riskTags:         []string{"context.long_range"},
		},
		{
			caseID:   "ablation-mix-009",
			taskType: "writing", difficulty: "L3",
			inputText: "撰写古籍数字化深度稿《给善本建一座桥》（约2200字）。覆盖：古籍数字化的技术链路（高清扫描、OCR 识别、异体字库、知识图谱）、'识典古籍'等公开平台案例、版权与整理者的署名之争。专有名词与案例名全文一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"技术链路四环节完整", "平台案例准确且一致", "涉及版权争议的平衡呈现"},
			mustNotHave:      []string{"链路环节缺失", "案例名前后不一", "争议表述失衡"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking", "cross_chapter_state"},
			riskTags:         []string{"entity.confusion", "context.long_range"},
		},
		{
			caseID:   "ablation-mix-010",
			taskType: "writing", difficulty: "L1",
			inputText: "撰写城市更新观察稿《老厂房的第二次生命》（约1500字）。以'首钢园'与'上海杨浦滨江'为案例，讨论工业遗存活化的三种模式（文化场馆、产业园区、公共空间），案例信息全文一致。",
			context:          map[string]interface{}{"article": ""},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"两个案例均有展开", "三种模式清晰", "案例信息一致"},
			mustNotHave:      []string{"案例张冠李戴", "模式划分混乱", "只有概念没有案例"},
			capabilityTags:   []string{"long_form_writing", "entity_tracking"},
			riskTags:         []string{"entity.confusion"},
		},
		// --- 多材料 frozen 补齐（9 例）---
		{
			caseID:   "ablation-mix-011",
			taskType: "writing", difficulty: "L2",
			inputText: "基于三份气候文献，撰写深度解读《1.5°C：一个数字的政治与科学》（约2200字）。要求：(1)准确引用三份文献的关键数据；(2)说明1.5°C目标的科学依据与达成难度；(3)观点平衡，不夸大单一来源。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "中国气候变化蓝皮书（2024）", "NASA全球气候变化关键指标（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三份文献均有引用", "关键数据准确", "平衡呈现科学与政治维度"},
			mustNotHave:       []string{"数据引用错误", "单一来源观点垄断", "编造未提供的结论"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity"},
			riskTags:          []string{"source.citation"},
		},
		{
			caseID:   "ablation-mix-012",
			taskType: "writing", difficulty: "L1",
			inputText: "基于三份市场报告，撰写《2025年新能源车融资环境研判》（约1800字）。要求：(1)综合三份报告的销量与渗透率数据；(2)指出数据口径差异并说明处理方式；(3)给出融资环境判断及理由。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"三份报告数据均有引用", "口径差异有说明", "融资判断有数据支撑"},
			mustNotHave:       []string{"忽略口径差异", "判断无依据", "数据张冠李戴"},
			capabilityTags:    []string{"multi_material_synthesis", "source_conflict_resolution"},
			riskTags:          []string{"source.conflict"},
		},
		{
			caseID:   "ablation-mix-013",
			taskType: "writing", difficulty: "L2",
			inputText: "基于三份AI治理文件，撰写一份《AI 产品上线合规自查清单》（约1500字）。要求：(1)条目化输出（每条含检查项与依据）；(2)依据须注明来自哪份文件；(3)清单可操作，避免空泛表述。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "美国AI行政命令与政策动态（2023-2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo"},
			expectedBehavior:  "answer",
			mustHave:          []string{"条目化清单", "每条注明依据来源", "覆盖三份文件的要求"},
			mustNotHave:       []string{"条目无依据", "清单不可操作", "遗漏重要合规项"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "compliance_writing"},
			riskTags:          []string{"source.citation"},
		},
		{
			caseID:   "ablation-mix-014",
			taskType: "writing", difficulty: "L1",
			inputText: "基于三份 API 文档，撰写《订单-支付链路联调指南》（约1800字）。要求：(1)按'下单→支付→回调'串联三个模块的接口；(2)端点与错误码与文档完全一致；(3)给出至少三个常见联调问题的排查步骤。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"链路串联完整", "端点与错误码准确", "三个排查步骤具体"},
			mustNotHave:       []string{"端点拼写错误", "错误码与文档不符", "链路顺序错乱"},
			capabilityTags:    []string{"multi_material_synthesis", "citation_fidelity", "technical_writing"},
			riskTags:          []string{"source.citation"},
		},
		{
			caseID:   "ablation-mix-015",
			taskType: "writing", difficulty: "L2",
			inputText: "基于三份季度财报，撰写董事会汇报材料《前三季度经营回顾》（约1500字）。要求：(1)管理层视角，突出趋势与风险；(2)所有财务数据与财报一致；(3)结尾给出四季度行动建议（不超过三条）。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"数据与财报一致", "有趋势与风险分析", "行动建议不超过三条"},
			mustNotHave:       []string{"数据错误", "建议超过三条", "风格不符合董事会材料"},
			capabilityTags:    []string{"multi_material_synthesis", "numerical_accuracy", "business_writing"},
			riskTags:          []string{"source.fabrication"},
		},
		{
			caseID:   "ablation-mix-016",
			taskType: "writing", difficulty: "L3",
			inputText: "基于气候与市场两类文献，撰写交叉分析《气候目标如何重塑汽车产业》（约2500字）。要求：(1)至少引用一份气候文献与两份市场报告；(2)建立'排放目标→产业政策→市场结构'的传导链条；(3)链条各环节的数据引用准确。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"IPCC第六次评估报告摘要（2023）", "Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"跨类文献均有引用", "传导链条完整", "环节数据准确"},
			mustNotHave:       []string{"链条断裂", "数据引用错误", "两类文献割裂"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis"},
			riskTags:          []string{"source.synthesis", "context.token_pressure"},
		},
		{
			caseID:   "ablation-mix-017",
			taskType: "writing", difficulty: "L2",
			inputText: "基于政策与市场两类文件，撰写《监管如何影响新能源车市场格局》（约2000字）。要求：(1)对照至少两份政策文件与两份市场报告；(2)识别监管变量对市场数据的影响机制；(3)不臆造政策内容。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"欧盟人工智能法案概述（2024）", "中国人工智能治理政策框架（2024）", "Gartner新能源汽车市场报告（2024）", "IDC新能源汽车市场追踪（2024）", "McKinsey出行行业报告（2024）"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-policy-eu-ai-act", "src-policy-china-ai", "src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey"},
			expectedBehavior:  "answer",
			mustHave:          []string{"政策与市场文献对照", "影响机制有逻辑", "不臆造政策条款"},
			mustNotHave:       []string{"虚构监管要求", "机制分析牵强", "文献引用错位"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "policy_analysis"},
			riskTags:          []string{"source.synthesis"},
		},
		{
			caseID:   "ablation-mix-018",
			taskType: "writing", difficulty: "L2",
			inputText: "基于 API 文档与季度财报，撰写《API 平台商业化复盘》（约2000字）。要求：(1)将 API 能力（用户/订单/支付模块）与业务数据对应；(2)评估开放能力对营收的贡献逻辑；(3)指出财报与文档中对不上的口径并标注。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"RESTful API设计规范v3.1 - 用户管理模块", "RESTful API设计规范v3.1 - 订单管理模块", "RESTful API设计规范v3.1 - 支付模块", "科技公司季度财报 - Q1数据", "科技公司季度财报 - Q2数据", "科技公司季度财报 - Q3数据"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3", "src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"API 与业务数据对应", "贡献逻辑有依据", "口径差异被标注"},
			mustNotHave:       []string{"强行归因", "口径差异被忽略", "文档与财报数据混淆"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "analytical_writing"},
			riskTags:          []string{"source.synthesis", "context.token_pressure"},
		},
		{
			caseID:   "ablation-mix-019",
			taskType: "writing", difficulty: "L3",
			inputText: "基于全部15份源材料，撰写《致股东信：在不确定中定价确定性》（约2500字）。要求：(1)以公司高管口吻，融合宏观（气候、政策）与经营（市场、财报）视角；(2)至少引用8份来源且注明；(3)避免年报套话，有具体判断。",
			context: map[string]interface{}{
				"article":   "",
				"materials": []string{"全部15份源材料"},
			},
			sourceMode:        "frozen",
			sourceFixtureRefs: []string{"src-climate-ipcc-2023", "src-climate-china-2024", "src-climate-nasa-2024", "src-market-ev-gartner", "src-market-ev-idc", "src-market-ev-mckinsey", "src-policy-eu-ai-act", "src-policy-china-ai", "src-policy-us-eo", "src-tech-rest-api-1", "src-tech-rest-api-2", "src-tech-rest-api-3", "src-biz-quarterly-1", "src-biz-quarterly-2", "src-biz-quarterly-3"},
			expectedBehavior:  "answer",
			mustHave:          []string{"宏观与经营视角融合", "至少引用8份来源", "有具体判断非套话"},
			mustNotHave:       []string{"来源引用不足", "通篇套话", "数据编造"},
			capabilityTags:    []string{"multi_material_synthesis", "cross_source_synthesis", "long_form_writing"},
			riskTags:          []string{"context.token_pressure", "source.synthesis"},
		},
		// --- polish 补齐（9 例）---
		{
			caseID:   "ablation-mix-020",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下内部邮件改写为致客户的正式公告（约400字）。要求：保留延期与补偿两个核心事实，补偿口径（'发放14天会员权益'）一字不改，语气诚恳专业，不出现内部吐槽表述。",
			context: map[string]interface{}{
				"article": "老张说这周稳定性那个事儿还得再拖一周，上周三的存储迁移出了岔子，周四又回滚了一次。老板的意思是这周五之前必须稳住。对外的说法统一为'服务升级延期至下周三完成'，补偿就按之前说的，给受影响用户发14天会员权益，别写多了。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"延期与补偿事实准确", "14天会员权益一字不改", "正式公告语气"},
			mustNotHave:      []string{"出现'出了岔子'等内部口吻", "补偿口径被改写", "遗漏延期说明"},
			capabilityTags:   []string{"faithful_rewrite", "audience_adaptation", "meaning_preservation"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-mix-021",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下访谈录音整理稿润色为可发布的专访文章（约900字）。要求：(1)保留受访者全部关键观点与数字（装机量、目标年份）；(2)删除口头禅与重复表述；(3)不得替受访者新增观点。",
			context: map[string]interface{}{
				"article": "受访者（某储能企业负责人）：呃……我们去年那个装机量是1.2吉瓦时，对，1.2吉瓦时，同比增长大概……应该是翻了一番吧。就是说这个行业，嗯，它其实最后拼的不是价格，是安全。对，安全。我们的目标呢，是2027年，把海外收入占比做到35%，呃，对，35%。我觉得这个还是，嗯，比较稳妥的一个目标。然后别的，暂时没有什么要补充的。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"装机量1.2吉瓦时与翻番表述保留", "2027年与35%目标准确", "无新增观点"},
			mustNotHave:      []string{"数字被改写", "替受访者加观点", "口头禅残留"},
			capabilityTags:   []string{"faithful_rewrite", "voice_preservation"},
			riskTags:         []string{"rewrite.voice_drift"},
		},
		{
			caseID:   "ablation-mix-022",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下专业菜谱改写为厨房新手也能跟着做的版本（约500字）。要求：所有用料克数与火候时间不得改动，把'壬水汆烫'更正为'沸水汆烫'这类术语替换为日常说法，步骤顺序不变。",
			context: map[string]interface{}{
				"article": "白灼菜心：菜心300g，洗净去老根。壬水汆烫：锅中注水2000ml，沸后加食盐5g、食用油10ml，入菜心汆烫45秒，捞出过冰水。豉油汁：蒸鱼豉油30ml、清水20ml、白糖2g，小火煮至微沸。装盘：菜心沥尽，淋豉油汁，葱丝姜丝各2g铺面，热油15ml烧至七成热（约210℃），浇淋激香。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"克数与时间全部保留", "术语有通俗替换", "步骤顺序不变"},
			mustNotHave:      []string{"用量或时间被改动", "步骤顺序变化", "术语未替换"},
			capabilityTags:   []string{"faithful_rewrite", "audience_adaptation", "meaning_preservation"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
		{
			caseID:   "ablation-mix-023",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下合同条款改写为消费者能读懂的白话解读（约400字）。硬性要求：不得改变条款的权利义务含义，关键数字（30日、5%等）必须原样保留，每条解读后注明'以合同原文为准'。",
			context: map[string]interface{}{
				"article": "第七条 交付与验收：乙方应于合同生效之日起三十（30）日内完成交付，甲方应于交付后十（10）个工作日内完成验收；逾期未提出书面异议的，视为验收合格。第八条 违约责任：任何一方违约的，应向守约方支付合同总价款百分之五（5%）的违约金；违约金不足以弥补损失的，守约方有权另行追偿。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"30日/10个工作日/5%原样保留", "权利义务含义不变", "注明'以合同原文为准'"},
			mustNotHave:      []string{"数字被改写", "义务含义被淡化", "遗漏免责提示"},
			capabilityTags:   []string{"faithful_rewrite", "meaning_preservation", "audience_adaptation"},
			riskTags:         []string{"rewrite.meaning_distortion"},
		},
		{
			caseID:   "ablation-mix-024",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下旅行攻略压缩为300字以内的速览版。要求：五天的行程骨架、交通方式与两处必吃店名必须保留，其余细节可省略，但保留的部分不得改变原信息。",
			context: map[string]interface{}{
				"article": "泉州五日行程（全文版）：D1 抵达，宿西街，傍晚逛开元寺东西塔；D2 清源山看日出，下山吃面线糊，下午游府文庙；D3 包车去惠安，看崇武古城，午餐尝崇武鱼卷；D4 上午蟳埔村体验簪花围，下午回城逛中山路骑楼，晚餐在西街的老字号面煎粿铺和石花膏店解决；D5 上午泉州海外交通史博物馆，下午返程。全程公共交通加一日包车，人均预算2200元。注意事项：簪花体验要提前一天预约；开元寺免费但需在公众号预约入场。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"五天骨架完整", "两处必吃店名保留", "300字以内", "保留信息与原文一致"},
			mustNotHave:      []string{"行程顺序改变", "店名写错或遗漏", "超过300字"},
			capabilityTags:   []string{"faithful_rewrite", "compression"},
			riskTags:         []string{"rewrite.information_loss"},
		},
		{
			caseID:   "ablation-mix-025",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下中文课程简介翻译为英文（约150词）。要求：课程名'数字叙事工作坊'译为 Digital Storytelling Workshop，八周时长译准确（an eight-week course），师资头衔（副教授）译法规范，所有数据（24学时）准确。",
			context: map[string]interface{}{
				"article": "数字叙事工作坊：本课程共八周、24学时，面向对跨媒体创作感兴趣的高年级本科生。课程将讲授故事结构、视觉语言与交互设计三大模块，结课作品为一部3-5分钟的互动叙事短片。主讲教师为传播学院副教授林岚，其作品曾获两届国际数字叙事节金奖。选课人数上限30人，需提交一份一页纸的作品构思作为选课材料。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"课程名译法规范", "八周与24学时准确", "副教授头衔译法得当"},
			mustNotHave:      []string{"数据翻译错误", "头衔误译", "遗漏选课要求"},
			capabilityTags:   []string{"faithful_rewrite", "translation"},
			riskTags:         []string{"rewrite.translation_error"},
		},
		{
			caseID:   "ablation-mix-026",
			taskType: "polish", difficulty: "L2",
			inputText: "将以下 SOP 文档改写为新员工培训话术（约600字）。要求：流程步骤与判定阈值不得改动（响应时长30分钟、升级阈值4小时），把条文语气转为'讲解者口吻'，关键数字需重复强调一次以便记忆。",
			context: map[string]interface{}{
				"article": "客服工单处理SOP：1）接单后须在30分钟内首次响应；2）一般问题48小时内闭环；3）超过4小时未解决的工单自动升级至值班主管；4）升级工单须在升级后2小时内给出处理方案；5）所有工单关闭前须完成用户回访。本SOP自2025年7月1日起执行，每周五抽查执行情况。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"30分钟与4小时阈值不变", "讲解者口吻", "关键数字重复强调"},
			mustNotHave:      []string{"阈值被改动", "仍是条文腔", "数字未强调"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation"},
			riskTags:         []string{"rewrite.voice_drift"},
		},
		{
			caseID:   "ablation-mix-027",
			taskType: "polish", difficulty: "L1",
			inputText: "将以下招聘 JD 改写为小红书风格的招聘帖（约300字）。要求：岗位（前端开发）、城市（杭州）、薪资区间（20-35K）三项硬信息不得改动或夸大，其余可改写为亲切口语，但不得承诺 JD 中没有的福利。",
			context: map[string]interface{}{
				"article": "招聘：前端开发工程师。工作地：杭州。薪资：20-35K·14薪。职责：负责数据可视化平台的前端架构与性能优化。要求：三年以上前端经验，熟练掌握 TypeScript 与主流框架，有大型可视化项目经验者优先。福利：五险一金、补充医疗、年度体检、15天年假。",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"岗位/城市/薪资准确", "小红书风格自然", "福利未超出原 JD"},
			mustNotHave:      []string{"薪资被夸大", "虚构福利", "三项硬信息缺失"},
			capabilityTags:   []string{"faithful_rewrite", "style_adaptation"},
			riskTags:         []string{"rewrite.hallucination"},
		},
		{
			caseID:   "ablation-mix-028",
			taskType: "polish", difficulty: "L3",
			inputText: "将以下带注释的代码片段说明改写为面向初中级开发者的教程（约700字）。要求：代码逻辑解释不得出错，配置参数（maxRetries=3、timeoutMs=2000）原样保留，教程需补充一段'常见错误'但不得与原注释矛盾。",
			context: map[string]interface{}{
				"article": "// 带退避的重试封装\n// maxRetries: 最大重试次数（不含首次调用）\n// timeoutMs: 单次调用超时，毫秒\nfunc WithRetry[T any](fn func() (T, error), maxRetries int, timeoutMs int) (T, error) {\n    // 指数退避：第 n 次重试等待 2^n * 100ms\n    // 注意：fn 必须是幂等的，否则重复调用可能产生副作用\n    var lastErr error\n    for attempt := 0; attempt <= maxRetries; attempt++ {\n        if attempt > 0 {\n            time.Sleep(backoff(attempt))\n        }\n        ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)\n        defer cancel()\n        result, err := invoke(ctx, fn)\n        if err == nil {\n            return result, nil\n        }\n        lastErr = err\n    }\n    var zero T\n    return zero, lastErr\n}",
			},
			sourceMode:       "none",
			expectedBehavior: "answer",
			mustHave:         []string{"退避与幂等要点解释正确", "参数名与数值原样保留", "常见错误不与原注释矛盾"},
			mustNotHave:      []string{"逻辑解释错误", "参数被改写", "新增内容与原注释冲突"},
			capabilityTags:   []string{"faithful_rewrite", "technical_accuracy", "audience_adaptation"},
			riskTags:         []string{"rewrite.factual_drift"},
		},
	}
}

// ── ablation candidates ──────────────────────────────────────────────────────

// seedAblationCandidates inserts the four ablation candidates (A-D) into
// wabench_candidates. It is idempotent: existing candidates are skipped.
func seedAblationCandidates(ctx context.Context, db *sql.DB) error {
	type candidateDef struct {
		candidateID string
		name        string
		flags       map[string]interface{}
	}
	defs := []candidateDef{
		{
			candidateID: "ablation-A-baseline",
			name:        "A: Baseline (no context, no memory)",
			flags: map[string]interface{}{
				"memoryEnabled":          false,
				"contextCompilerEnabled": false,
				"projectMemoryEnabled":   false,
			},
		},
		{
			candidateID: "ablation-B-context",
			name:        "B: Context Compiler only",
			flags: map[string]interface{}{
				"memoryEnabled":          false,
				"contextCompilerEnabled": true,
				"projectMemoryEnabled":   false,
			},
		},
		{
			candidateID: "ablation-C-context-memory",
			name:        "C: Context + User Memory",
			flags: map[string]interface{}{
				"memoryEnabled":          true,
				"memoryUserId":           ablationMemoryUserID,
				"contextCompilerEnabled": true,
				"projectMemoryEnabled":   false,
			},
		},
		{
			candidateID: "ablation-D-full",
			name:        "D: Context + User Memory + Project Memory",
			flags: map[string]interface{}{
				"memoryEnabled":          true,
				"memoryUserId":           ablationMemoryUserID,
				"contextCompilerEnabled": true,
				"projectMemoryEnabled":   true,
			},
		},
	}

	modelManifest := map[string]interface{}{"model": "deepseek-v3-flash"}
	modelJSON, _ := json.Marshal(modelManifest)
	toolJSON, _ := json.Marshal(map[string]interface{}{})
	promptHash := sha256Hex("ablation-prompt-v1")
	codeHash := sha256Hex("ablation-code-v1")

	inserted := 0
	for _, d := range defs {
		flagsJSON, _ := json.Marshal(d.flags)
		// Candidates with memoryEnabled=true require a memoryHash.
		memHash := ""
		if enabled, _ := d.flags["memoryEnabled"].(bool); enabled {
			memHash = sha256Hex("ablation-memory-v1")
		}
		res, err := db.ExecContext(ctx, `
			INSERT INTO wabench_candidates (
				candidate_id, schema_version, name, prompt_hash, memory_hash,
				model_manifest, code_hash, tool_manifest, feature_flags
			) VALUES ($1, 'wabench.v1', $2, $3, NULLIF($4, ''), $5, $6, $7, $8)
			ON CONFLICT (candidate_id) DO NOTHING
		`, d.candidateID, d.name, promptHash, memHash,
			modelJSON, codeHash, toolJSON, flagsJSON)
		if err != nil {
			return fmt.Errorf("insert candidate %s: %w", d.candidateID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
			fmt.Printf("  candidate %s: created\n", d.candidateID)
		} else {
			fmt.Printf("  candidate %s: already exists\n", d.candidateID)
		}
	}
	fmt.Printf("Ablation candidates: %d new (of %d total)\n", inserted, len(defs))
	return nil
}

// ── ablation runs ────────────────────────────────────────────────────────────

// ablationCandidateIDs returns the ordered list of ablation candidate IDs.
func ablationCandidateIDs() []string {
	return []string{
		"ablation-A-baseline",
		"ablation-B-context",
		"ablation-C-context-memory",
		"ablation-D-full",
	}
}

// seedAblationRuns creates one WABench run per ablation candidate against the
// ablation-benchmark-v1 suite. It is idempotent: existing runs are skipped.
func seedAblationRuns(ctx context.Context, db *sql.DB) error {
	// Look up suite PK.
	var suitePK string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text FROM wabench_suites WHERE suite_id = $1`, suiteID,
	).Scan(&suitePK); err != nil {
		return fmt.Errorf("suite %s not found (run default seed first): %w", suiteID, err)
	}

	inserted := 0
	for _, candidateID := range ablationCandidateIDs() {
		// Look up candidate PK.
		var candidatePK string
		if err := db.QueryRowContext(ctx,
			`SELECT id::text FROM wabench_candidates WHERE candidate_id = $1`, candidateID,
		).Scan(&candidatePK); err != nil {
			return fmt.Errorf("candidate %s not found (run --seed-candidates first): %w", candidateID, err)
		}

		runID := "ablation-run-" + candidateID
		res, err := db.ExecContext(ctx, `
			INSERT INTO wabench_runs (
				run_id, schema_version, suite_pk, candidate_pk, adapter_id, runner_version,
				environment, traffic_type, status, total_cases
			) VALUES (
				$1, 'wabench.v1', $2, $3, 'luminbuddy-v2', 'wabench.v1',
				'ablation', 'replay', 'pending',
				(SELECT COUNT(*) FROM wabench_cases WHERE suite_pk = $2)
			)
			ON CONFLICT (run_id) DO NOTHING
		`, runID, suitePK, candidatePK)
		if err != nil {
			return fmt.Errorf("insert run %s: %w", runID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
			fmt.Printf("  run %s: created\n", runID)
		} else {
			fmt.Printf("  run %s: already exists\n", runID)
		}
	}
	fmt.Printf("Ablation runs: %d new (of %d total)\n", inserted, len(ablationCandidateIDs()))
	return nil
}

// ── ablation runs v2（200+ 用例消融重跑）─────────────────────────────────────

// v2RunDateSuffix 是 v2 重跑 run_id 的日期后缀（计划执行日 2026-09-23），
// 固定写死以保证幂等：重复执行 --seed-runs-v2 不会产生第二组 run。
const v2RunDateSuffix = "20260923"

// seedAblationRunsV2 为四个消融候选各创建一个 v2 pending run，run_id 形如
// ablation-run-v2-A-baseline-20260923。run 绑定现有 suite 与 candidate，
// total_cases 取 suite 当前实际用例数；已存在则跳过（幂等）。
func seedAblationRunsV2(ctx context.Context, db *sql.DB) error {
	// Look up suite PK.
	var suitePK string
	if err := db.QueryRowContext(ctx,
		`SELECT id::text FROM wabench_suites WHERE suite_id = $1`, suiteID,
	).Scan(&suitePK); err != nil {
		return fmt.Errorf("suite %s not found (run default seed first): %w", suiteID, err)
	}

	inserted := 0
	for _, candidateID := range ablationCandidateIDs() {
		// Look up candidate PK.
		var candidatePK string
		if err := db.QueryRowContext(ctx,
			`SELECT id::text FROM wabench_candidates WHERE candidate_id = $1`, candidateID,
		).Scan(&candidatePK); err != nil {
			return fmt.Errorf("candidate %s not found (run --seed-candidates first): %w", candidateID, err)
		}

		// ablation-A-baseline -> ablation-run-v2-A-baseline-20260923
		v2RunID := "ablation-run-v2-" + strings.TrimPrefix(candidateID, "ablation-") + "-" + v2RunDateSuffix
		res, err := db.ExecContext(ctx, `
			INSERT INTO wabench_runs (
				run_id, schema_version, suite_pk, candidate_pk, adapter_id, runner_version,
				environment, traffic_type, status, total_cases
			) VALUES (
				$1, 'wabench.v1', $2, $3, 'luminbuddy-v2', 'wabench.v1',
				'ablation', 'replay', 'pending',
				(SELECT COUNT(*) FROM wabench_cases WHERE suite_pk = $2)
			)
			ON CONFLICT (run_id) DO NOTHING
		`, v2RunID, suitePK, candidatePK)
		if err != nil {
			return fmt.Errorf("insert run %s: %w", v2RunID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
			fmt.Printf("  run %s: created\n", v2RunID)
		} else {
			fmt.Printf("  run %s: already exists\n", v2RunID)
		}
	}
	fmt.Printf("Ablation v2 runs: %d new (of %d total)\n", inserted, len(ablationCandidateIDs()))
	return nil
}

// ── start-runs helper ────────────────────────────────────────────────────────

// printStartRunCommands prints the curl commands to trigger execution of each
// ablation run via the admin API. The seed script creates the database rows;
// actual execution requires the running server with a configured LLM adapter.
func printStartRunCommands() {
	fmt.Println()
	fmt.Println("Ablation candidates and runs are seeded. To execute each run, call the admin API:")
	fmt.Println()
	for _, candidateID := range ablationCandidateIDs() {
		runID := "ablation-run-" + candidateID
		fmt.Printf("  # %s\n", candidateID)
		fmt.Printf("  curl -X POST http://localhost:8080/api/v2/admin/wabench/runs \\\n")
		fmt.Printf("    -H 'Content-Type: application/json' \\\n")
		fmt.Printf("    -d '{\"suiteId\":%q,\"candidateId\":%q,\"environment\":\"ablation\"}'\n\n", suiteID, candidateID)
		_ = runID // used above for reference; actual execution goes through the API
	}
	fmt.Println("Or trigger all runs programmatically by hitting the endpoint for each candidate.")
}

// ── status ───────────────────────────────────────────────────────────────────

// showAblationStatus queries and displays the current state of all ablation runs.
func showAblationStatus(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.run_id, r.status, r.total_cases, r.completed_cases, r.failed_cases,
		       c.candidate_id, c.name,
		       COALESCE(AVG((rv.evidence->>'weightedScore')::double precision), 0) AS avg_score
		FROM wabench_runs r
		JOIN wabench_candidates c ON c.id = r.candidate_pk
		LEFT JOIN wabench_outputs o ON o.run_pk = r.id
		LEFT JOIN wabench_reviews rv ON rv.output_pk = o.id
		WHERE r.run_id LIKE 'ablation-run-%'
		GROUP BY r.run_id, r.status, r.total_cases, r.completed_cases, r.failed_cases,
		         c.candidate_id, c.name
		ORDER BY r.run_id
	`)
	if err != nil {
		fail("query ablation runs: %v", err)
	}
	defer rows.Close()

	fmt.Println()
	fmt.Printf("%-42s %-10s %-6s %-10s %-8s %-8s %s\n",
		"RUN ID", "STATUS", "TOTAL", "COMPLETED", "FAILED", "SCORE", "CANDIDATE")
	fmt.Println(strings.Repeat("-", 110))

	found := false
	for rows.Next() {
		found = true
		var runID, status, candidateID, candidateName string
		var total, completed, failed int
		var avgScore float64
		if err := rows.Scan(&runID, &status, &total, &completed, &failed, &candidateID, &candidateName, &avgScore); err != nil {
			fail("scan ablation run: %v", err)
		}
		scoreStr := "-"
		if completed > 0 {
			scoreStr = fmt.Sprintf("%.2f", avgScore)
		}
		fmt.Printf("%-42s %-10s %-6d %-10d %-8d %-8s %s\n",
			runID, status, total, completed, failed, scoreStr, candidateName)
	}
	if err := rows.Err(); err != nil {
		fail("iterate ablation runs: %v", err)
	}
	if !found {
		fmt.Println("  No ablation runs found. Run with --start-runs first.")
	}
	fmt.Println()
}

// ── memory seeding（C/D 候选的记忆库种子）───────────────────────────────────

// ablationMemoryUserID 是消融 C/D 候选（feature_flags.memoryUserId）使用的
// 记忆用户 ID。该用户是纯种子用户：WABench 执行走 RunCore（契约无记忆写
// 入），库若为空则 C/D 的记忆注入从未有内容可注入——必须先用 --seed-memories
// 播种。seedAblationCandidates 中的两处引用与这里必须保持同一常量。
const ablationMemoryUserID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

// seedMemory 是一条待播种的 Tier1 硬偏好。CaseRef 记录它呼应哪条消融用例：
// 要么是该用例 inputText 里声称「记忆里记录…」的既有偏好（explicit_override
// 用例的显式指令正是对它的反转/推翻），要么是 memory_isolation 用例要求隔离
// 的世界观/项目语境。
type seedMemory struct {
	Category string
	Key      string
	Value    string
	CaseRef  string // 对应的 ablation case ID；无直接对应用例时为空
}

// ablationSeedMemories 返回播种清单（12 条，术语/世界观/篇幅/结构/语体各居
// 其位）。内容刻意通过 PII 语义检查：不含电话、地址、邮箱、证件号等敏感
// 信息；Tier1 硬偏好经 SDK.Create 写入（confidence 1.0、每轮必注入，且免
// 证据门——RequireVerifiedForWriting 默认 false）。
func ablationSeedMemories() []seedMemory {
	return []seedMemory{
		{
			// override-011 的显式新指令正是反转它：全文统一使用「AI」。
			Category: "terminology", Key: "term_ai_naming",
			Value:   "科技写作术语偏好：全文使用『人工智能』而非『AI』",
			CaseRef: "ablation-override-011",
		},
		{
			// override-017 的 memory_isolation 用例要求把这个世界观隔离在本任务之外。
			Category: "topic", Key: "wuxia_worldview",
			Value:   "题材偏好：创作武侠小说，作品设定在内力、门派、轻功的传统武侠世界观",
			CaseRef: "ablation-override-017",
		},
		{
			Category: "word_count", Key: "prefers_long_form",
			Value:   "篇幅偏好：单篇 2000 字起步，喜欢铺陈背景与细节",
			CaseRef: "",
		},
		{
			Category: "structure", Key: "bullet_points",
			Value:   "喜欢分点论述：用『1. 2. 3.』编号列表组织观点",
			CaseRef: "ablation-override-001",
		},
		{
			Category: "structure", Key: "conclusion_first",
			Value:   "常用『结论先行』结构：开篇先给总结句",
			CaseRef: "ablation-override-004",
		},
		{
			Category: "style", Key: "emoji_usage",
			Value:   "喜欢在文案里适当使用表情符号",
			CaseRef: "ablation-override-003",
		},
		{
			Category: "tone", Key: "self_reference_xiaobian",
			Value:   "自称习惯用『小编』",
			CaseRef: "ablation-override-005",
		},
		{
			Category: "style", Key: "long_complex_sentences",
			Value:   "偏好繁复的长句文风，多用排比与从句",
			CaseRef: "ablation-override-009",
		},
		{
			Category: "tone", Key: "ending_call_to_action",
			Value:   "习惯在文章结尾加行动号召（如『立即咨询』）",
			CaseRef: "ablation-override-006",
		},
		{
			Category: "style", Key: "british_spelling",
			Value:   "英文行文偏好英式拼写：organise、colour",
			CaseRef: "ablation-override-007",
		},
		{
			Category: "terminology", Key: "term_mini_program",
			Value:   "习惯把『小程序』写作『微信小程序』",
			CaseRef: "ablation-override-008",
		},
		{
			Category: "title", Key: "question_style",
			Value:   "标题爱用问句",
			CaseRef: "ablation-override-010",
		},
	}
}

// seedStore 抽象播种器需要的两个操作，使幂等逻辑可以脱离 Postgres 单测。
// 真实实现 memsvcSeedStore 走 internal/memory 的装配：
//
//   - findActive → PgStore.FindByCategoryKey（只认 active/candidate 状态）
//   - createHardPreference → Service.Create → SDK.Create：Tier1 硬偏好、
//     confidence 1.0、PII 检查（CLI 侧 sensitiveCheck 为 nil → Noop 放行）、
//     embedding 失败不阻塞保存（Gate union 召回兜底）
type seedStore interface {
	findActive(ctx context.Context, userID, category, key string) (bool, error)
	createHardPreference(ctx context.Context, userID, category, key, value string) error
}

// seedMemoriesInto 把种子逐条写入 userID 的记忆库。幂等：按 category+key
// 查重，已存在（active/candidate）即跳过、绝不重复插入。
// 返回 (created, skipped) 便于二次执行断言 0 新增。
func seedMemoriesInto(ctx context.Context, st seedStore, userID string, seeds []seedMemory, logf func(string, ...interface{})) (int, int, error) {
	created, skipped := 0, 0
	for _, s := range seeds {
		exists, err := st.findActive(ctx, userID, s.Category, s.Key)
		if err != nil {
			return created, skipped, fmt.Errorf("lookup %s/%s: %w", s.Category, s.Key, err)
		}
		if exists {
			skipped++
			logf("  %-12s/%-24s already exists (skipped)  <- %s", s.Category, s.Key, caseRefLabel(s))
			continue
		}
		if err := st.createHardPreference(ctx, userID, s.Category, s.Key, s.Value); err != nil {
			return created, skipped, fmt.Errorf("create %s/%s: %w", s.Category, s.Key, err)
		}
		created++
		logf("  %-12s/%-24s created                    <- %s", s.Category, s.Key, caseRefLabel(s))
	}
	return created, skipped, nil
}

func caseRefLabel(s seedMemory) string {
	if s.CaseRef == "" {
		return "(baseline preference)"
	}
	return "echoes " + s.CaseRef
}

// memsvcSeedStore 是 seedStore 的真实实现，走真实记忆服务装配。
type memsvcSeedStore struct {
	db  *database.DB
	svc *memsvc.Service
}

func (s *memsvcSeedStore) findActive(ctx context.Context, userID, category, key string) (bool, error) {
	existing, err := memsvc.NewPgStore(s.db).FindByCategoryKey(ctx, userID, category, key)
	if err != nil {
		return false, err
	}
	return len(existing) > 0, nil
}

func (s *memsvcSeedStore) createHardPreference(ctx context.Context, userID, category, key, value string) error {
	_, err := s.svc.Create(ctx, userID, category, key, value)
	return err
}

// seedAblationMemories 为 ablationMemoryUserID 播种 Tier1 硬偏好。装配与
// cmd/run-ablation 的 buildMemoryPort 同款：memsvc.NewService(db, llm,
// embedding, nil)，LLM 走与 runner 相同的环境变量契约
// （LLM_API_KEY / LLM_BASE_URL / LLM_MODEL），DASHSCOPE_* 未配置时 embedding
// 传 nil（SDK.Create 对 nil embedder 安全降级，只跳过向量持久化）。
// 注意：Create 路径不调用 LLM——LLM_API_KEY 缺失仅影响提取链，不影响播种，
// 因此这里只告警不 fail-fast。
func seedAblationMemories(ctx context.Context, dbURL string) error {
	memDB, err := database.NewPostgres(dbURL, 5, 2)
	if err != nil {
		return fmt.Errorf("open memory database: %w", err)
	}
	defer memDB.Close()

	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		log.Println("WARN [seed-memories] LLM_API_KEY not set: the Create path writes Tier1 " +
			"preferences directly without calling the LLM, so seeding proceeds; extraction would be unavailable")
	}
	llm := tools.NewLLMClient(
		envOrDefault("LLM_BASE_URL", "https://api.xiaomimimo.com/v1"),
		apiKey,
		envOrDefault("LLM_MODEL", "mimo-v2.5"),
		32768, 0.7, 60*time.Second,
	)

	embeddingClient := tools.NewEmbeddingClient(
		os.Getenv("DASHSCOPE_API_KEY"),
		envOrDefault("DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
		envOrDefault("DASHSCOPE_MODEL", "text-embedding-v3"),
		1024,
	)
	if !embeddingClient.IsConfigured() {
		log.Println("WARN [seed-memories] DASHSCOPE_API_KEY not set: seeded memories will have no " +
			"embedding (semantic recall skips them; keyword/recency union recall still works)")
		embeddingClient = nil
	}

	memSvc := memsvc.NewService(memDB, llm, embeddingClient, nil)
	if memSvc == nil || !memSvc.IsAvailable() {
		return fmt.Errorf("memory service unavailable (db unreachable)")
	}

	// user_memories.user_id 外键指向 users——消融记忆用户是合成 UUID，
	// 必须先确保桩用户行存在（幂等；仅 id/uid/role 非空字段，无法登录）。
	if _, err := memDB.Exec(
		`INSERT INTO users (id, uid, role) VALUES ($1::uuid, $2, 'user') ON CONFLICT (id) DO NOTHING`,
		ablationMemoryUserID, "ablation-memory-user"); err != nil {
		return fmt.Errorf("ensure bench memory user: %w", err)
	}

	st := &memsvcSeedStore{db: memDB, svc: memSvc}
	fmt.Printf("Seeding %d Tier1 hard preferences for memory user %s...\n",
		len(ablationSeedMemories()), ablationMemoryUserID)
	created, skipped, err := seedMemoriesInto(ctx, st, ablationMemoryUserID, ablationSeedMemories(),
		func(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) })
	if err != nil {
		return err
	}
	fmt.Printf("Memory seeding done: %d created, %d skipped (idempotent by category+key)\n", created, skipped)
	return nil
}

// wipeAblationMemories 删除记忆用户的全部记忆痕迹：memory_session_dismissals
//（经 user_memories 子查询关联）、memory_entities、user_memories。全部参数
// 绑定（$1），不做字符串拼接。
func wipeAblationMemories(ctx context.Context, dbURL string) error {
	memDB, err := database.NewPostgres(dbURL, 5, 2)
	if err != nil {
		return fmt.Errorf("open memory database: %w", err)
	}
	defer memDB.Close()

	steps := []struct {
		label string
		sql   string
	}{
		{
			label: "memory_session_dismissals",
			sql: `DELETE FROM memory_session_dismissals
				  WHERE memory_id IN (SELECT id FROM user_memories WHERE user_id = $1::uuid)`,
		},
		{label: "memory_entities", sql: `DELETE FROM memory_entities WHERE user_id = $1::uuid`},
		{label: "user_memories", sql: `DELETE FROM user_memories WHERE user_id = $1::uuid`},
	}
	for _, step := range steps {
		res, err := memDB.ExecContext(ctx, step.sql, ablationMemoryUserID)
		if err != nil {
			return fmt.Errorf("delete %s: %w", step.label, err)
		}
		n, _ := res.RowsAffected()
		fmt.Printf("  %-26s deleted %d rows\n", step.label, n)
	}
	fmt.Printf("Memory wipe done for user %s\n", ablationMemoryUserID)
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── helpers ──────────────────────────────────────────────────────────────────

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

func pqStringArray(items []string) string {
	if len(items) == 0 {
		return "{}"
	}
	escaped := make([]string, len(items))
	for i, s := range items {
		escaped[i] = `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` // quote + escape backslashes
	}
	return "{" + strings.Join(escaped, ",") + "}"
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "seed-ablation-cases: "+format+"\n", args...)
	os.Exit(1)
}
