package cli

// BoardUIStrings holds every user-visible string rendered by the Web UI
// templates.  The default locale is zh-CN (Simplified Chinese).  Adding a new
// locale means copying this struct with translated values; no template change
// is required as long as the field names stay the same.
//
// The struct is exported so the test package can inspect locale coverage.
type BoardUIStrings struct {
	// Brand / header
	ProductName       string // "Agent Board"
	BoardSubtitle     string // "AI 研发任务看板"
	GeneratedAt       string // "生成于 {{.Time}}"
	FooterSuffix      string // "· 无需网络"
	IssueDetailSuffix string // "· 任务详情"

	// New Task modal
	NewTask             string // "+ 新建任务"
	NewTaskModalHeading string // "新建任务"
	CreateTask          string // "创建任务"
	Cancel              string // "取消"

	// Form fields
	FieldTitle              string // "标题"
	FieldDescription        string // "任务说明"
	FieldAcceptanceCriteria string // "验收标准"
	FieldPriority           string // "优先级"
	FieldStatus             string // "状态"
	FieldDefaultPriority    string // "默认（中）"
	StatusOpen              string // "待处理"
	StatusReady             string // "就绪"

	// Edit / detail
	EditTask      string // "编辑任务"
	SaveChanges   string // "保存修改"
	MoveToReady   string // "移入待开始"
	ReturnToBoard string // "返回看板"

	// Board columns — English suffix preserved for recognisability
	ColReady      string // "待开始 READY"
	ColInProgress string // "进行中 IN PROGRESS"
	ColDone       string // "已完成 DONE"
	ColBlocked    string // "已阻塞 BLOCKED"

	// Card detail labels
	ReadyRank        string // "待开始排名"
	LeaseExpiry      string // "租约结束于"
	ReviewLabel      string // "验收"
	ChangesRequested string // "轮修改请求"
	DeliveryCommit   string // "提交"
	DeliveryBranch   string // "分支"
	DeliveryPR       string // "合并请求"

	// Card actions
	MoveUp   string // "上移"
	MoveDown string // "下移"

	// Card executor role
	RoleDeveloper string // "开发者"
	RoleVerifier  string // "验证者"
	RoleExecutor  string // "执行者"

	// Collapsible auxiliary panel
	ActivityDiagnostics string // "活动与诊断"

	// Search panel
	Search            string // "搜索"
	SearchPlaceholder string // "搜索..."
	SearchBtn         string // "搜索"
	LoadMore          string // "加载更多"
	SearchAll         string // "全部"
	SearchIssue       string // "任务"
	SearchComment     string // "评论"
	SearchDecision    string // "决策"
	SearchReview      string // "验收"
	SearchAttemptNote string // "执行备注"
	NoOwningIssue     string // "无所属任务"
	SearchInitialHelp string // "输入搜索关键词查找任务、评论、决策、验收和执行备注"
	SearchNoResults   string // "未找到结果"
	SearchInvalid     string // "搜索查询无效"
	SearchUnavail     string // "搜索暂时不可用"

	// Status counts section
	StatusCounts string // "状态统计"
	Count        string // "数量"

	// Active attempts section
	ActiveAttempts          string // "执行中任务"
	AttemptID               string // "执行 ID"
	AttemptIssue            string // "任务"
	AttemptTitle            string // "标题"
	AttemptKind             string // "类型"
	AttemptSession          string // "会话"
	AttemptInstance         string // "实例"
	AttemptClient           string // "客户端"
	AttemptModel            string // "模型"
	AttemptWorktree         string // "工作目录"
	AttemptStarted          string // "开始时间"
	AttemptLease            string // "租约到期"
	AttemptReservations     string // "资源预留"
	AttemptGates            string // "关卡"
	NoActiveAttempts        string // "暂无执行中任务"
	ActiveAttemptsTruncated string // "仅显示前 100 个执行中任务"
	ReservationsTruncated   string // "仅显示前 100 个资源预留"

	// Resource reservation summary
	ReservationSummary string // "{{.Count}} 个活跃资源预留，按所属执行分组展示"

	// Blocked issues section
	BlockedIssues          string // "阻塞任务"
	BlockedIssue           string // "任务"
	BlockedTitle           string // "标题"
	BlockedReasonCol       string // "阻塞原因"
	NoBlockedIssues        string // "暂无阻塞任务"
	BlockedIssuesTruncated string // "仅显示前 100 个阻塞任务"

	// Review requests section
	OpenReviewRequests      string // "待验收"
	ReviewRequestIDCol      string // "请求 ID"
	ReviewIssueCol          string // "任务"
	ReviewStatusCol         string // "状态"
	ReviewTargetVersionCol  string // "目标版本"
	ReviewCreatedAtCol      string // "创建时间"
	NoOpenReviewRequests    string // "暂无待验收"
	ReviewRequestsTruncated string // "仅显示前 100 个验收请求"

	// Planning graph section
	PlanningGraph  string // "任务关系"
	MermaidSource  string // "Mermaid 源码（复制到任一 Mermaid 渲染器）"
	GraphNodeCount string // "{{.Nodes}} 个节点，{{.Edges}} 条边，{{.Entries}} 个入口点，{{.Blocking}} 个阻塞节点"
	GraphTruncated string // "（已截断）"

	// Empty states
	NoCards     string // "暂无卡片"
	NoIssuesYet string // "暂无任务"

	// Truncation notices
	TruncatedReady               string // "待开始卡片已截断，可能存在更多"
	TruncatedInProgress          string // "进行中卡片已截断，可能存在更多"
	TruncatedBlocked             string // "已阻塞卡片已截断，可能存在更多"
	TruncatedDone                string // "已完成卡片已截断，可能存在更多"
	TruncatedReview              string // "验收信号已截断，卡片验收状态可能不是最新的"
	TruncatedDelivery            string // "部分卡片的交付引用已截断，打开任务查看完整列表"
	TruncatedDeliveryUnavailable string // "部分卡片的交付引用无法读取"
	TruncatedUnprojected         string // "仅显示前 100 个未归入看板的任务"

	// Unprojected section
	UnprojectedSummary string // "未在看板中展示（{{.Count}}）"
	UnprojectedDesc    string // "这些任务是真实项目状态，但不属于任何看板列，因此看板如实报告而非猜测其所属列"
	UnprojectedIssue   string // "任务"
	UnprojectedTitle   string // "标题"
	UnprojectedStatus  string // "存储状态"
	UnprojectedReason  string // "原因"
	UnprojectedDetail  string // "详情"
	UnprojectedAction  string // "操作"
	UnprojectedQueue   string // "移入待开始"

	// Issue detail page
	Metadata               string // "元数据"
	Labels                 string // "标签"
	NoLabels               string // "未分配标签"
	WorkflowGates          string // "工作流关卡"
	GatesNoneApply         string // "该任务无需通过工作流关卡"
	GatesSatisfied         string // "所有关卡条件均已满足"
	GatesStatusLine        string // "在 {{.Point}} 节点评估（基于 {{.Source}}）：{{.Satisfied}}/{{.Total}} 个条件满足"
	LatestAttemptSection   string // "最近执行"
	OpenReviewSection      string // "验收中"
	LatestDecisionSection  string // "最新决策"
	RelatedGraph           string // "相关关系图"
	ReservationsSection    string // "资源预留"
	NoReservations         string // "无历史或当前资源预留"
	AdditionalReservations string // "存在更多资源预留"
	RecentActivity         string // "最近动态"
	NoActivity             string // "暂无活动记录"
	AdditionalActivity     string // "存在更多活动记录"
	VersionLabel           string // "版本"
	CreatedLabel           string // "创建时间"
	UpdatedLabel           string // "更新时间"
	ArchivedLabel          string // "归档时间"
	NotArchived            string // "未归档"

	// Detail empty values
	NoDescription        string // "未提供任务说明"
	NoAcceptanceCriteria string // "未提供验收标准"
	NoBlockedReason      string // "未提供阻塞原因"

	// Status line on issue detail
	StatusLine string // "存储状态：{{.Stored}} · 有效状态：{{.Effective}} · 类型：{{.Type}} · 优先级：{{.Priority}}"

	// Gate detail text
	GateSourceLive     string // "实时策略"
	GateSourceSnapshot string // "执行快照（指纹 {{.Fingerprint}}）"

	// Root issue section
	RootIssueSection string // "根任务"

	// Gate table columns
	GateRequirementCol string // "需求"
	GateReasonCol      string // "原因"
	GateNextActionCol  string // "下一步操作"

	// Col heading (default column titles for the board)
	DefaultColReady      string
	DefaultColInProgress string
	DefaultColDone       string
	DefaultColBlocked    string

	// Unprojected default statuses
	DefaultStatus string // "状态"
}

// boardUIStringsZHCN returns the default zh-CN locale.
func boardUIStringsZHCN() BoardUIStrings {
	return BoardUIStrings{
		ProductName:       "Agent Board",
		BoardSubtitle:     "AI 研发任务看板",
		GeneratedAt:       "生成于",
		FooterSuffix:      "· 无需网络",
		IssueDetailSuffix: "· 任务详情",

		NewTask:             "+ 新建任务",
		NewTaskModalHeading: "新建任务",
		CreateTask:          "创建任务",
		Cancel:              "取消",

		FieldTitle:              "标题",
		FieldDescription:        "任务说明",
		FieldAcceptanceCriteria: "验收标准",
		FieldPriority:           "优先级",
		FieldStatus:             "状态",
		FieldDefaultPriority:    "默认（中）",
		StatusOpen:              "待处理",
		StatusReady:             "就绪",

		EditTask:      "编辑任务",
		SaveChanges:   "保存修改",
		MoveToReady:   "移入待开始",
		ReturnToBoard: "返回看板",

		ColReady:      "待开始 READY",
		ColInProgress: "进行中 IN PROGRESS",
		ColDone:       "已完成 DONE",
		ColBlocked:    "已阻塞 BLOCKED",

		ReadyRank:        "待开始排名",
		LeaseExpiry:      "租约结束于",
		ReviewLabel:      "验收",
		ChangesRequested: "轮修改请求",
		DeliveryCommit:   "提交",
		DeliveryBranch:   "分支",
		DeliveryPR:       "合并请求",

		MoveUp:   "上移",
		MoveDown: "下移",

		RoleDeveloper: "开发者",
		RoleVerifier:  "验证者",
		RoleExecutor:  "执行者",

		ActivityDiagnostics: "活动与诊断",

		Search:            "搜索",
		SearchPlaceholder: "搜索...",
		SearchBtn:         "搜索",
		LoadMore:          "加载更多",
		SearchAll:         "全部",
		SearchIssue:       "任务",
		SearchComment:     "评论",
		SearchDecision:    "决策",
		SearchReview:      "验收",
		SearchAttemptNote: "执行备注",
		NoOwningIssue:     "无所属任务",
		SearchInitialHelp: "输入搜索关键词查找任务、评论、决策、验收和执行备注",
		SearchNoResults:   "未找到结果",
		SearchInvalid:     "搜索查询无效",
		SearchUnavail:     "搜索暂时不可用",

		StatusCounts: "状态统计",
		Count:        "数量",

		ActiveAttempts:          "执行中任务",
		AttemptID:               "执行 ID",
		AttemptIssue:            "任务",
		AttemptTitle:            "标题",
		AttemptKind:             "类型",
		AttemptSession:          "会话",
		AttemptInstance:         "实例",
		AttemptClient:           "客户端",
		AttemptModel:            "模型",
		AttemptWorktree:         "工作目录",
		AttemptStarted:          "开始时间",
		AttemptLease:            "租约到期",
		AttemptReservations:     "资源预留",
		AttemptGates:            "关卡",
		NoActiveAttempts:        "暂无执行中任务",
		ActiveAttemptsTruncated: "仅显示前 100 个执行中任务",
		ReservationsTruncated:   "仅显示前 100 个资源预留",

		ReservationSummary: "活跃资源预留按所属执行分组展示",

		BlockedIssues:          "阻塞任务",
		BlockedIssue:           "任务",
		BlockedTitle:           "标题",
		BlockedReasonCol:       "阻塞原因",
		NoBlockedIssues:        "暂无阻塞任务",
		BlockedIssuesTruncated: "仅显示前 100 个阻塞任务",

		OpenReviewRequests:      "待验收",
		ReviewRequestIDCol:      "请求 ID",
		ReviewIssueCol:          "任务",
		ReviewStatusCol:         "状态",
		ReviewTargetVersionCol:  "目标版本",
		ReviewCreatedAtCol:      "创建时间",
		NoOpenReviewRequests:    "暂无待验收",
		ReviewRequestsTruncated: "仅显示前 100 个验收请求",

		PlanningGraph: "任务关系",
		MermaidSource: "Mermaid 源码（复制到任一 Mermaid 渲染器）",

		NoCards:     "暂无卡片",
		NoIssuesYet: "暂无任务",

		TruncatedReady:               "待开始卡片已截断，可能存在更多",
		TruncatedInProgress:          "进行中卡片已截断，可能存在更多",
		TruncatedBlocked:             "已阻塞卡片已截断，可能存在更多",
		TruncatedDone:                "已完成卡片已截断，可能存在更多",
		TruncatedReview:              "验收信号已截断，卡片验收状态可能不是最新的",
		TruncatedDelivery:            "部分卡片的交付引用已截断，打开任务查看完整列表",
		TruncatedDeliveryUnavailable: "部分卡片的交付引用无法读取",
		TruncatedUnprojected:         "仅显示前 100 个未归入看板的任务",

		UnprojectedSummary: "未在看板中展示",
		UnprojectedDesc:    "这些任务是真实项目状态，但不属于任何看板列，因此看板如实报告而非猜测其所属列",
		UnprojectedIssue:   "任务",
		UnprojectedTitle:   "标题",
		UnprojectedStatus:  "存储状态",
		UnprojectedReason:  "原因",
		UnprojectedDetail:  "详情",
		UnprojectedAction:  "操作",
		UnprojectedQueue:   "移入待开始",

		Metadata:               "元数据",
		Labels:                 "标签",
		NoLabels:               "未分配标签",
		WorkflowGates:          "工作流关卡",
		GatesNoneApply:         "该任务无需通过工作流关卡",
		GatesSatisfied:         "所有关卡条件均已满足",
		LatestAttemptSection:   "最近执行",
		OpenReviewSection:      "验收中",
		LatestDecisionSection:  "最新决策",
		RelatedGraph:           "相关关系图",
		ReservationsSection:    "资源预留",
		NoReservations:         "无历史或当前资源预留",
		AdditionalReservations: "存在更多资源预留",
		RecentActivity:         "最近动态",
		NoActivity:             "暂无活动记录",
		AdditionalActivity:     "存在更多活动记录",
		VersionLabel:           "版本",
		CreatedLabel:           "创建时间",
		UpdatedLabel:           "更新时间",
		ArchivedLabel:          "归档时间",
		NotArchived:            "未归档",

		NoDescription:        "未提供任务说明",
		NoAcceptanceCriteria: "未提供验收标准",
		NoBlockedReason:      "未提供阻塞原因",

		GateSourceLive:     "实时策略",
		GateSourceSnapshot: "执行快照（指纹）",
		RootIssueSection:   "根任务",
		GateRequirementCol: "需求",
		GateReasonCol:      "原因",
		GateNextActionCol:  "下一步操作",
		DefaultStatus:      "状态",
	}
}
