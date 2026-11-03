package config

import (
	"fmt"
	"strconv"
)

// attachmentLimitsFromEnv keeps the deployment envelope bounded even when an
// operator overrides the per-file or per-project capacity.
func attachmentLimitsFromEnv(c *Config) error {
	file, err := resolveEnv("HELM_ATTACHMENT_MAX_FILE_BYTES")
	if err != nil {
		return err
	}
	quota, err := resolveEnv("HELM_ATTACHMENT_PROJECT_QUOTA_BYTES")
	if err != nil {
		return err
	}
	c.AttachmentMaxFileBytes = 10 << 20
	c.AttachmentProjectQuotaBytes = 1 << 30
	if file.value != "" {
		n, e := strconv.ParseInt(file.value, 10, 64)
		if e != nil || n < 1 || n > 40<<20 {
			return fmt.Errorf("HELM_ATTACHMENT_MAX_FILE_BYTES must be an integer from 1 through 41943040")
		}
		c.AttachmentMaxFileBytes = n
	}
	if quota.value != "" {
		n, e := strconv.ParseInt(quota.value, 10, 64)
		if e != nil || n < 1 || n > 1<<40 {
			return fmt.Errorf("HELM_ATTACHMENT_PROJECT_QUOTA_BYTES must be an integer from 1 through 1099511627776")
		}
		c.AttachmentProjectQuotaBytes = n
	}
	return nil
}
