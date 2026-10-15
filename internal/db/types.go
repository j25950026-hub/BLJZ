package db

import "context"

type GroupConfig struct {
	ChatID                 int64
	Title                  string
	OwnerUserID            int64
	BookkeepingEnabled     int
	BookkeepingCycleKey    string
	DisplayNickname        int
	ShowUSDTConversion     int
	FeeRate                float64
	PayoutFeeRate          float64
	CustomRate             float64
	PayoutRate             float64
	UseRealtimeRate        int
	RateSource             string
	DayCutTime             string
	PayoutAddress          string
	PayoutAddressRemark    string
	ExclusiveAddress       string
	ExclusiveAddressOwner  string
	ExclusiveAddressRemark string
	GuaranteeRule          string
	GroupType              string
	DuplicateAlertEnabled  int
	GroupTags              string
	AtBroadcastEnabled     int
	AutoReplyEnabled       int
	CustomContentWithTime  int
	ClassStartAd           string
	ClassEndAd             string
	WelcomeEnabled         int
	WelcomeText            string
	LedgerCardHideReversed int
}

type AdminGroupDictionaryItem struct {
	Kind        string
	Name        string
	Description string
	SortOrder   int
	UpdatedAt   string
}

type GroupClosingReminderConfig struct {
	ChatID       int64
	Enabled      int
	ReminderTime string
	ThresholdU   float64
	DMMode       string
	TextNormal   string
	TextPending  string
	TextNoData   string
	TextReview   string
	TextFailed   string
	UpdatedBy    int64
	UpdatedAt    string
}

type GroupClosingDMRecipient struct {
	ChatID   int64
	UserID   int64
	Username string
	Nickname string
	AddedBy  int64
	AddedAt  string
}

type ExclusiveAddressEntry struct {
	ID        int64
	ChatID    int64
	Address   string
	Owner     string
	Remark    string
	CreatedAt string
	UpdatedAt string
}

type LedgerEntryInput struct {
	ChatID           int64
	MessageID        int
	UserID           int64
	Username         string
	Nickname         string
	EntryType        string
	Amount           float64
	Currency         string
	BaseAmount       float64
	FeeRate          float64
	Rate             float64
	FinalAmount      float64
	Note             string
	CardNo           string
	TrackMessageLink string
	ReversalOf       *int64
}

type LedgerEntry struct {
	ID          int64
	ChatID      int64
	MessageID   int
	UserID      int64
	Username    string
	Nickname    string
	EntryType   string
	Amount      float64
	Currency    string
	BaseAmount  float64
	FeeRate     float64
	Rate        float64
	FinalAmount float64
	Note        string
	CardNo      string
	TrackLink   string
	CreatedAt   string
	Reversed    int
	ReversalOf  *int64
}

type OrderSearchQuery struct {
	ChatID  int64
	Keyword string
	Amount  *float64
	StartAt string
	Limit   int
}

type OrderSearchBackend interface {
	IndexLedgerEntry(ctx context.Context, entry LedgerEntry) error
	SearchLedgerEntryIDs(ctx context.Context, query OrderSearchQuery) ([]int64, error)
}

type LedgerApplyResult struct {
	EntryID                int64
	CardSeenCount          int
	RecentAmountSeenCount  int
	RecentCardSeenCount    int
	RecentAddressSeenCount int
	RecentAddress          string
	Duplicate              bool
}

type LedgerReverseResult struct {
	OriginalID int64
	ReversalID int64
	Duplicate  bool
	NotFound   bool
	Settled    bool
}

type GroupUser struct {
	ChatID     int64
	UserID     int64
	Username   string
	Nickname   string
	LastSeenAt string
}

type GroupUserAlias struct {
	UserID       int64
	Username     string
	SourceChatID int64
	FirstSeenAt  string
	LastSeenAt   string
}

type GroupUserProfileDiff struct {
	UsernameChanged bool
	NicknameChanged bool
	OldUsername     string
	NewUsername     string
	OldNickname     string
	NewNickname     string
}

type IdentityUser struct {
	UserID   int64
	Username string
}

type Schedule struct {
	ID              int64
	ChatID          int64
	SendAt          string
	Content         string
	SourceChatID    int64
	SourceMessageID int
	IdentityName    string
	TargetTags      string
	Status          string
	CreatorUserID   int64
}

type Operator struct {
	UserID   int64
	Username string
	IsGlobal int
}

type GlobalOwner struct {
	UserID   int64
	Username string
}

type AutoReply struct {
	ID        int64
	ChatID    int64
	Keyword   string
	Content   string
	Enabled   int
	CreatedAt string
}

type ForwardRule struct {
	Keyword       string
	TargetChatIDs string
}

type CustomContent struct {
	TriggerKey string
	Content    string
	CreatedBy  int64
	CreatedAt  string
}

type AdminSavedView struct {
	OwnerUsername string
	Name          string
	Filters       map[string]string
	UpdatedAt     string
}

type AdminChangeSnapshot struct {
	ID            int64
	OwnerUsername string
	Action        string
	SummaryJSON   string
	Status        string
	CreatedAt     string
	Items         []AdminChangeSnapshotItem
}

type AdminChangeSnapshotItem struct {
	ID            int64
	SnapshotID    int64
	ChatID        int64
	BeforeJSON    string
	AfterJSON     string
	Status        string
	FailureReason string
	CreatedAt     string
}

type AdminTemplateLibrary struct {
	ID            int64
	Kind          string
	Name          string
	Description   string
	Version       int
	ContentJSON   string
	OwnerUsername string
	CreatedAt     string
}

type AdminRetryItem struct {
	ID            int64
	OperationID   string
	ItemKey       string
	Kind          string
	ChatID        int64
	PayloadJSON   string
	FailureReason string
	Status        string
	AttemptCount  int
	CreatedAt     string
	UpdatedAt     string
}

type AdminSetupState struct {
	SingletonID           int64
	ConsumedAt            int64
	ConsumedBy            string
	CredentialFingerprint string
	AuthEpoch             int64
	CreatedAt             int64
	UpdatedAt             int64
}

type AdminSessionRecord struct {
	ID            int64
	SessionHandle string
	TokenHash     string
	Username      string
	AuthEpoch     int64
	IsPrimary     bool
	CreatedAt     int64
	LastSeenAt    int64
	ExpiresAt     *int64
	UserAgent     string
	RemoteAddr    string
	RevokedAt     *int64
}

type AdminSessionSummary struct {
	SessionHandle string
	Username      string
	AuthEpoch     int64
	IsPrimary     bool
	CreatedAt     int64
	LastSeenAt    int64
	ExpiresAt     *int64
	UserAgent     string
	RemoteAddr    string
	IsCurrent     bool
}

type AdminDeviceBinding struct {
	ID                     int64
	BindingID              string
	TokenHash              string
	Username               string
	InitiatorSessionHandle string
	TargetCookieHash       string
	Status                 string
	StatusVersion          int
	TargetUserAgent        string
	TargetRemoteAddr       string
	CreatedAt              int64
	ClaimedAt              *int64
	ApprovedAt             *int64
	ExpiresAt              int64
	CompletedAt            *int64
}

type IdentityAssignment struct {
	IdentityName string
	UserID       int64
	Username     string
	CreatedAt    string
}

type DelayedQuery struct {
	ID      int64
	Keyword string
	Amount  *float64
	Note    string
	Status  string
}

type BranchCodeOverride struct {
	ChatID     int64
	CardNo     string
	CNAPSCode  string
	BranchName string
	Note       string
	UpdatedBy  int64
	UpdatedAt  string
}

type PrivateUserProfile struct {
	UserID         int64
	TrialStatus    string
	TrialAppliedAt string
	ExpireAt       string
	UpdatedAt      string
}

type PrivatePurchaseOrderInput struct {
	UserID       int64
	PlanName     string
	DurationDays int
	Amount       float64
	Currency     string
	RequestNote  string
	Source       string
}

type PrivatePurchaseOrder struct {
	ID           int64
	UserID       int64
	PlanName     string
	DurationDays int
	Amount       float64
	Currency     string
	Status       string
	RequestNote  string
	ReviewNote   string
	Source       string
	ExpiresAt    string
	ReviewedBy   int64
	ReviewedAt   string
	CreatedAt    string
	UpdatedAt    string
}

type AddressVerificationSnapshot struct {
	ChatID                 int64
	Address                string
	VerificationCount      int
	PreviousSenderUserID   int64
	PreviousSenderUsername string
	LastSenderUserID       int64
	LastSenderUsername     string
	LastVerifiedAt         string
}

type AddressVerificationMessageRecord struct {
	ChatID          int64
	MessageID       int
	SenderUserID    int64
	SenderUsername  string
	OriginalAddress string
	CurrentAddress  string
	SentAt          string
	FirstSeenAt     string
	LastEditAt      string
	EditCount       int
	UpdatedAt       string
}

type OutboundDeadLetterInput struct {
	ChatID       int64
	Priority     int
	Operation    string
	MessageType  string
	Payload      string
	ErrorMessage string
}

type OutboundDeadLetter struct {
	ID           int64
	ChatID       int64
	Priority     int
	Operation    string
	MessageType  string
	Payload      string
	ErrorMessage string
	CreatedAt    string
}

type LedgerReceiptOutboxInput struct {
	ChatID          int64
	SourceMessageID int
	EntryID         int64
	UserID          int64
	CommandKey      string
	Payload         string
}

type LedgerReceiptOutbox struct {
	ID                int64
	ChatID            int64
	SourceMessageID   int
	EntryID           int64
	UserID            int64
	CommandKey        string
	Payload           string
	Status            string
	AttemptCount      int
	LastError         string
	NextAttemptAt     string
	ProcessingStarted string
	SentAt            string
	CreatedAt         string
	UpdatedAt         string
}

type DailySettlementCurrencySummary struct {
	Currency     string  `json:"currency"`
	EntryCount   int     `json:"entry_count"`
	DepositCount int     `json:"deposit_count"`
	PayoutCount  int     `json:"payout_count"`
	DepositTotal float64 `json:"deposit_total"`
	PayoutTotal  float64 `json:"payout_total"`
	NetProfit    float64 `json:"net_profit"`
}

type DailySettlementReport struct {
	ChatID            int64
	ReportDate        string
	PeriodStart       string
	PeriodEnd         string
	TotalEntries      int
	DepositCount      int
	PayoutCount       int
	DepositTotal      float64
	PayoutTotal       float64
	NetProfit         float64
	CurrencyBreakdown []DailySettlementCurrencySummary
	GeneratedAt       string
}

type DailySettlementBatchResult struct {
	ReportDate       string
	PeriodStart      string
	PeriodEnd        string
	TotalGroups      int
	GeneratedReports int
	SkippedGroups    int
}

type DayCutSettlementCurrencySummary struct {
	Currency        string  `json:"currency"`
	EntryCount      int     `json:"entry_count"`
	DepositCount    int     `json:"deposit_count"`
	PayoutCount     int     `json:"payout_count"`
	DepositFeeTotal float64 `json:"deposit_fee_total"`
	PayoutFeeTotal  float64 `json:"payout_fee_total"`
	NetFeeTotal     float64 `json:"net_fee_total"`
}

type DayCutSettlementReport struct {
	ChatID            int64
	ReportDate        string
	DayCutTime        string
	PeriodStart       string
	PeriodEnd         string
	TotalEntries      int
	DepositCount      int
	PayoutCount       int
	DepositFeeTotal   float64
	PayoutFeeTotal    float64
	NetFeeTotal       float64
	CurrencyBreakdown []DayCutSettlementCurrencySummary
	GeneratedAt       string
}

type DayCutSettlementBatchResult struct {
	RunAt            string
	Timezone         string
	TotalGroups      int
	GeneratedReports int
	ForcedReports    int
	DeficitReports   int
	SkippedGroups    int
}

type ClosingReminderSummary struct {
	ChatID       int64
	BusinessDate string
	PeriodStart  string
	PeriodEnd    string
	Status       string
	DepositCount int
	PayoutCount  int
	DepositTotal float64
	PayoutTotal  float64
	PendingTotal float64
	GeneratedAt  string
}
