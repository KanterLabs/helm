package store

const (
	PortableBundleFormat         = "helm.portable.bundle"
	PortableBundleVersion        = 5
	PortableBundleMaxBytes int64 = 256 << 20
	portableBundleMaxFiles       = 1002
	portableBundleMaxJSON  int64 = 32 << 20
)

// PortableBundleManifest wraps the stable v4 JSON contract. Only visible,
// published images are portable; deleted, staged and deleted-comment images
// remain local. Previews are regenerated from verified original bytes.
type PortableBundleManifest struct {
	Format        string                     `json:"format"`
	Version       int                        `json:"version"`
	Archive       string                     `json:"archive"`
	ArchiveSHA256 string                     `json:"archive_sha256"`
	Attachments   []PortableBundleAttachment `json:"attachments"`
}
type PortableBundleAttachment struct {
	Attachment Attachment `json:"attachment"`
	Object     string     `json:"object"`
	CommentID  string     `json:"comment_id,omitempty"`
	ProgressID string     `json:"progress_id,omitempty"`
	LinkedAt   string     `json:"linked_at"`
	Position   int        `json:"position"`
}
