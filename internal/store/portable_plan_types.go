package store

type portableProjectPlan struct {
	source PortableProject
	id     string
	key    string
	slug   string
	create bool
}

type portableColumnPlan struct {
	source    PortableColumn
	id        string
	projectID string
	position  int
	create    bool
}

type portableReleasePlan struct {
	source     PortableRelease
	id         string
	projectID  string
	releasedBy *string
	create     bool
}

type portableLabelPlan struct {
	source    PortableLabel
	id        string
	projectID string
	create    bool
}

type portableTaskPlan struct {
	source         PortableTask
	id             string
	projectID      string
	columnID       string
	number         int
	assigneeID     *string
	claimedBy      *string
	claimExpiresAt *string
	create         bool
}

type portableCommentPlan struct {
	source  PortableComment
	id      string
	taskID  string
	actorID string
	create  bool
}

type portableAgentNotePlan struct {
	source  PortableAgentNote
	id      string
	taskID  string
	actorID string
	create  bool
}

type portableDependencyPlan struct {
	source         PortableDependency
	taskID         string
	prerequisiteID string
	createdBy      *string
	create         bool
}

type portableDependencyNode struct {
	ID            string
	ProjectID     string
	SemanticState string
	Live          bool
	Completed     bool
	ClaimActive   bool
}

type portableLinkPlan struct {
	source       PortableTaskLink
	sourceTaskID string
	targetTaskID string
	create       bool
}

type portableEventPlan struct {
	source    PortableEvent
	id        string
	actorID   *string
	projectID *string
	taskID    *string
	create    bool
}

type portableEventPayloadMaps struct {
	artifacts map[string]string
	audits    map[string]string
	findings  map[string]string
	projects  map[string]string
	releases  map[string]string
	tasks     map[string]string
	actors    map[string]string
	comments  map[string]string
	columns   map[string]string
	labels    map[string]string
}

type portableWorkPlan struct {
	source  PortableAgentWork
	taskID  string
	actorID string
	create  bool
}

type portableHistoryPlan struct {
	source             PortableAgentWorkHistory
	id                 string
	taskID             string
	actorID            string
	generatedCommentID *string
	create             bool
}

type portableImportPlan struct {
	github         portableGithubPlan
	archive        PortableArchive
	report         PortableImportReport
	options        PortableImportOptions
	projects       []portableProjectPlan
	columns        []portableColumnPlan
	releases       []portableReleasePlan
	labels         []portableLabelPlan
	tasks          []portableTaskPlan
	taskLabels     []PortableTaskLabel
	dependencies   []portableDependencyPlan
	links          []portableLinkPlan
	comments       []portableCommentPlan
	agentNotes     []portableAgentNotePlan
	events         []portableEventPlan
	work           []portableWorkPlan
	history        []portableHistoryPlan
	projectMap     map[string]string
	columnMap      map[string]string
	releaseMap     map[string]string
	taskMap        map[string]string
	labelMap       map[string]string
	commentMap     map[string]string
	eventCursorMap map[int64]int64
}
