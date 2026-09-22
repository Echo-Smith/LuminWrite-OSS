// Command seed-ablation-cases populates the WP3 Context & Memory ablation
// benchmark dataset into the WABench tables.
//
// Usage:
//
//	go run ./cmd/seed-ablation-cases/                  # seed suite, fixtures, cases
//	go run ./cmd/seed-ablation-cases/ --seed-candidates # seed ablation candidates A-D
//	go run ./cmd/seed-ablation-cases/ --seed-runs       # create ablation runs (requires candidates)
//	go run ./cmd/seed-ablation-cases/ --start-runs      # candidates + runs + print execution commands
//	go run ./cmd/seed-ablation-cases/ --status           # show ablation run progress
//
// The script is idempotent: it uses ON CONFLICT to skip already-seeded rows.
// It reads DATABASE_URL (or TEST_DATABASE_URL) from the environment.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
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
		) VALUES ($1, 'wabench.v1', $2, $3, $4, $5, $6, 'active', 90, $7, $8)
		ON CONFLICT (suite_id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			updated_at = NOW()
		RETURNING id::text
	`, suiteID, suiteVersion,
		"WP3 Context & Memory Ablation Benchmark",
		"90-case ablation dataset for testing context compilation, memory isolation, and through-line consistency across long-form creation, multi-material synthesis, and faithful rewrite tasks.",
		partition, visibility, coverage, privacy,
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

	cases := buildAllCases()
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

// ── 90 cases ─────────────────────────────────────────────────────────────────

func buildAllCases() []ablationCase {
	var cases []ablationCase
	cases = append(cases, buildCategoryA()...)
	cases = append(cases, buildCategoryB()...)
	cases = append(cases, buildCategoryC()...)
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
				"memoryUserId":           "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
				"contextCompilerEnabled": true,
				"projectMemoryEnabled":   false,
			},
		},
		{
			candidateID: "ablation-D-full",
			name:        "D: Context + User Memory + Project Memory",
			flags: map[string]interface{}{
				"memoryEnabled":          true,
				"memoryUserId":           "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
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
