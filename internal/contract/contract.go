// Package contract owns frozen v1 limits and the fixed Slack route matrix.
package contract

import "time"

const (
	EnvelopeVersion = 1

	MaxTokenBytes              = 8 << 10
	MaxMessageRunes            = 4_000
	MaxMessageBytes            = 16 << 10
	MaxRequestBodyBytes        = 64 << 10
	MaxCompressedResponseBytes = 2 << 20
	MaxResponseBytes           = 8 << 20
	MaxStdoutBytes             = 8 << 20
	MaxStderrBytes             = 4 << 10
	MaxCursorBytes             = 4 << 10
	MaxProfileNameBytes        = 64
	MaxProfiles                = 100
	MaxPolicyTargets           = 500
	MaxRegistryBytes           = 1 << 20
	MaxSkillManifestBytes      = 64 << 10
	MaxSkillFiles              = 64
	MaxSkillFileBytes          = 1 << 20
	MaxSkillTotalBytes         = 4 << 20
	MaxSkillEntries            = 128
	MaxSkillPathBytes          = 512
	MaxSkillDepth              = 8
	MaxSlackIDBytes            = 64
	MaxSlackTimestampBytes     = 64
	MinCollectionLimit         = 1
	MaxCollectionLimit         = 100
	MinRetryAfterSeconds       = 1
	MaxRetryAfterSeconds       = 300

	NetworkDeadline       = 15 * time.Second
	WriteDeadline         = 15 * time.Second
	ReconcileDeadline     = 15 * time.Second
	ProductionSlackOrigin = "https://slack.com"
)

var allowedRoutes = [...]string{
	"auth.test",
	"chat.postMessage",
	"conversations.history",
	"conversations.info",
	"conversations.list",
	"conversations.replies",
	"users.info",
}

// Routes returns a copy of the fixed Slack method allowlist.
func Routes() []string { return append([]string(nil), allowedRoutes[:]...) }

// Limits is the machine-readable frozen v1 limits contract.
type Limits struct {
	TokenBytes              int   `json:"token_bytes"`
	MessageRunes            int   `json:"message_runes"`
	MessageBytes            int   `json:"message_bytes"`
	RequestBodyBytes        int   `json:"request_body_bytes"`
	CompressedResponseBytes int   `json:"compressed_response_bytes"`
	ResponseBytes           int   `json:"response_bytes"`
	StdoutBytes             int   `json:"stdout_bytes"`
	StderrBytes             int   `json:"stderr_bytes"`
	CursorBytes             int   `json:"cursor_bytes"`
	ProfileNameBytes        int   `json:"profile_name_bytes"`
	Profiles                int   `json:"profiles"`
	PolicyTargets           int   `json:"policy_targets"`
	RegistryBytes           int   `json:"registry_bytes"`
	SkillManifestBytes      int   `json:"skill_manifest_bytes"`
	SkillFiles              int   `json:"skill_files"`
	SkillFileBytes          int   `json:"skill_file_bytes"`
	SkillTotalBytes         int   `json:"skill_total_bytes"`
	SkillEntries            int   `json:"skill_entries"`
	SkillPathBytes          int   `json:"skill_path_bytes"`
	SkillDepth              int   `json:"skill_depth"`
	SlackIDBytes            int   `json:"slack_id_bytes"`
	SlackTimestampBytes     int   `json:"slack_timestamp_bytes"`
	CollectionLimitMin      int   `json:"collection_limit_min"`
	CollectionLimitMax      int   `json:"collection_limit_max"`
	RetryAfterMinSeconds    int   `json:"retry_after_min_seconds"`
	RetryAfterMaxSeconds    int   `json:"retry_after_max_seconds"`
	NetworkDeadlineMS       int64 `json:"network_deadline_ms"`
	WriteDeadlineMS         int64 `json:"write_deadline_ms"`
	ReconcileDeadlineMS     int64 `json:"reconcile_deadline_ms"`
}

// V1Limits returns a value copy of the frozen v1 bounds.
func V1Limits() Limits {
	return Limits{
		TokenBytes: MaxTokenBytes, MessageRunes: MaxMessageRunes,
		MessageBytes: MaxMessageBytes, RequestBodyBytes: MaxRequestBodyBytes,
		CompressedResponseBytes: MaxCompressedResponseBytes,
		ResponseBytes:           MaxResponseBytes, StdoutBytes: MaxStdoutBytes,
		StderrBytes: MaxStderrBytes, CursorBytes: MaxCursorBytes,
		ProfileNameBytes: MaxProfileNameBytes, Profiles: MaxProfiles,
		PolicyTargets: MaxPolicyTargets, SlackIDBytes: MaxSlackIDBytes,
		RegistryBytes: MaxRegistryBytes, SkillManifestBytes: MaxSkillManifestBytes,
		SkillFiles: MaxSkillFiles, SkillFileBytes: MaxSkillFileBytes,
		SkillTotalBytes: MaxSkillTotalBytes, SkillEntries: MaxSkillEntries,
		SkillPathBytes: MaxSkillPathBytes, SkillDepth: MaxSkillDepth,
		SlackTimestampBytes:  MaxSlackTimestampBytes,
		CollectionLimitMin:   MinCollectionLimit,
		CollectionLimitMax:   MaxCollectionLimit,
		RetryAfterMinSeconds: MinRetryAfterSeconds,
		RetryAfterMaxSeconds: MaxRetryAfterSeconds,
		NetworkDeadlineMS:    NetworkDeadline.Milliseconds(),
		WriteDeadlineMS:      WriteDeadline.Milliseconds(),
		ReconcileDeadlineMS:  ReconcileDeadline.Milliseconds(),
	}
}
