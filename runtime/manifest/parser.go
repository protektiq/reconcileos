package manifest

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxTextFieldLength      = 256
	maxDescriptionLength    = 2048
	maxTriggers             = 64
	maxAllowedEgressDomains = 64
	maxTimeoutSeconds       = 600
)

var (
	errEmptyManifest = errors.New("manifest content must not be empty")
	domainPattern    = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)
)

type PricingTier string

const (
	PricingTierFree PricingTier = "free"
	PricingTierPaid PricingTier = "paid"
)

type BotManifest struct {
	Name              string      `yaml:"name"`
	Version           string      `yaml:"version"`
	Description       string      `yaml:"description"`
	Author            string      `yaml:"author"`
	Triggers          []string    `yaml:"triggers"`
	Binary            string      `yaml:"binary"`
	PricingTier       PricingTier `yaml:"pricing_tier"`
	PricePerExecution float64     `yaml:"price_per_execution"`
	MaxTimeoutSeconds int         `yaml:"max_timeout_seconds"`
	AllowedEgress     []string    `yaml:"allowed_egress"`
}

func Parse(content []byte) (BotManifest, error) {
	if len(content) == 0 {
		return BotManifest{}, errEmptyManifest
	}

	var parsed BotManifest
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return BotManifest{}, fmt.Errorf("parse manifest yaml: %w", err)
	}

	if err := parsed.Validate(); err != nil {
		return BotManifest{}, err
	}

	return parsed, nil
}

func (m BotManifest) Validate() error {
	if err := validateTextField("name", m.Name, true, maxTextFieldLength); err != nil {
		return err
	}
	if err := validateTextField("version", m.Version, true, maxTextFieldLength); err != nil {
		return err
	}
	if err := validateTextField("description", m.Description, false, maxDescriptionLength); err != nil {
		return err
	}
	if err := validateTextField("author", m.Author, true, maxTextFieldLength); err != nil {
		return err
	}

	if len(m.Triggers) == 0 || len(m.Triggers) > maxTriggers {
		return fmt.Errorf("triggers must contain between 1 and %d event types", maxTriggers)
	}
	for i, trigger := range m.Triggers {
		if err := validateTextField(fmt.Sprintf("triggers[%d]", i), trigger, true, maxTextFieldLength); err != nil {
			return err
		}
	}

	if err := validateTextField("binary", m.Binary, true, 1024); err != nil {
		return err
	}
	if !filepath.IsAbs(strings.TrimSpace(m.Binary)) {
		return errors.New("binary must be an absolute path")
	}

	if m.PricingTier != PricingTierFree && m.PricingTier != PricingTierPaid {
		return errors.New("pricing_tier must be either 'free' or 'paid'")
	}
	if m.PricePerExecution < 0 {
		return errors.New("price_per_execution must be non-negative")
	}
	if m.MaxTimeoutSeconds <= 0 || m.MaxTimeoutSeconds > maxTimeoutSeconds {
		return fmt.Errorf("max_timeout_seconds must be between 1 and %d", maxTimeoutSeconds)
	}

	if len(m.AllowedEgress) > maxAllowedEgressDomains {
		return fmt.Errorf("allowed_egress must not exceed %d entries", maxAllowedEgressDomains)
	}
	for i, domain := range m.AllowedEgress {
		if err := validateDomain(fmt.Sprintf("allowed_egress[%d]", i), domain); err != nil {
			return err
		}
	}

	return nil
}

func validateTextField(fieldName, value string, required bool, maxLen int) error {
	trimmed := strings.TrimSpace(value)
	if required && trimmed == "" {
		return fmt.Errorf("%s is required", fieldName)
	}
	if len(trimmed) > maxLen {
		return fmt.Errorf("%s exceeds max length %d", fieldName, maxLen)
	}
	return nil
}

func validateDomain(fieldName, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s must not be empty", fieldName)
	}
	if len(trimmed) > 253 {
		return fmt.Errorf("%s exceeds max domain length", fieldName)
	}
	if !domainPattern.MatchString(trimmed) {
		return fmt.Errorf("%s has invalid format", fieldName)
	}
	if net.ParseIP(trimmed) != nil {
		return fmt.Errorf("%s must be a domain, not an IP address", fieldName)
	}
	return nil
}
