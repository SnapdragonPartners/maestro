package bootstrap

import (
	"fmt"
	"strings"
)

// SupportedPlatform represents a platform that Maestro can bootstrap.
type SupportedPlatform struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Keywords    []string `json:"keywords"`    // Keywords in specs that suggest this platform
	Confidence  float64  `json:"confidence"`  // Base confidence score (0.0-1.0)
	Stable      bool     `json:"stable"`      // Whether this platform is considered stable
	Description string   `json:"description"` // Human-readable description
	Versions    []string `json:"versions"`    // Supported versions
}

// PlatformRecommendation represents an architect's platform recommendation.
type PlatformRecommendation struct {
	Platform   string            `json:"platform"`
	Confidence float64           `json:"confidence"`
	Rationale  string            `json:"rationale"`
	Versions   map[string]string `json:"versions"`    // platform -> version mapping
	MultiStack bool              `json:"multi_stack"` // Whether this is a multi-platform project
	Platforms  []string          `json:"platforms"`   // All platforms for multi-stack projects
}

// PlatformWhitelist contains all supported platforms.
var PlatformWhitelist = map[string]SupportedPlatform{ //nolint:gochecknoglobals // Platform configuration data used throughout package
	"go": {
		Name:        "go",
		DisplayName: "Go",
		Keywords:    []string{"go", "golang", "api", "server", "backend", "microservice", "rest", "grpc"},
		Confidence:  0.9,
		Stable:      true,
		Description: "Go programming language for backend services and APIs",
		Versions:    []string{"1.24", "1.23", "1.22", "1.21"},
	},
	"node": {
		Name:        "node",
		DisplayName: "Node.js",
		Keywords:    []string{"node", "nodejs", "javascript", "js", "express", "fastify", "api", "backend"},
		Confidence:  0.9,
		Stable:      true,
		Description: "Node.js runtime for JavaScript backend services",
		Versions:    []string{"22", "20", "18", "16"},
	},
	"python": {
		Name:        "python",
		DisplayName: "Python",
		Keywords:    []string{"python", "django", "flask", "fastapi", "api", "ml", "machine learning", "data", "backend"},
		Confidence:  0.9,
		Stable:      true,
		Description: "Python programming language for backend services and data processing",
		Versions:    []string{"3.12", "3.11", "3.10", "3.9"},
	},
	"react": {
		Name:        "react",
		DisplayName: "React",
		Keywords:    []string{"react", "frontend", "web", "ui", "spa", "javascript", "typescript", "jsx", "tsx"},
		Confidence:  0.9,
		Stable:      true,
		Description: "React framework for frontend web applications",
		Versions:    []string{"18", "17", "16"},
	},
	"make": {
		Name:        "make",
		DisplayName: "Make",
		Keywords:    []string{"make", "makefile", "build", "generic", "custom"},
		Confidence:  0.7,
		Stable:      true,
		Description: "Generic Make-based build system",
		Versions:    []string{"latest"},
	},
	"null": {
		Name:        "null",
		DisplayName: "Unknown/No Platform",
		Keywords:    []string{},
		Confidence:  0.1,
		Stable:      true,
		Description: "Default fallback for unknown or no platform",
		Versions:    []string{"latest"},
	},
	// Future platforms (lower confidence, experimental)
	"rust": {
		Name:        "rust",
		DisplayName: "Rust",
		Keywords:    []string{"rust", "cargo", "backend", "systems", "performance"},
		Confidence:  0.6,
		Stable:      false,
		Description: "Rust programming language for systems programming",
		Versions:    []string{"1.70", "1.69", "1.68"},
	},
	"java": {
		Name:        "java",
		DisplayName: "Java",
		Keywords:    []string{"java", "spring", "maven", "gradle", "backend", "enterprise"},
		Confidence:  0.6,
		Stable:      false,
		Description: "Java programming language for enterprise applications",
		Versions:    []string{"21", "17", "11"},
	},
	"docker": {
		Name:        "docker",
		DisplayName: "Docker",
		Keywords:    []string{"docker", "container", "containerize", "dockerfile", "compose"},
		Confidence:  0.8,
		Stable:      true,
		Description: "Docker containerization platform",
		Versions:    []string{"latest", "24", "23"},
	},
}

// GetSupportedPlatforms returns all supported platforms.
func GetSupportedPlatforms() map[string]SupportedPlatform {
	return PlatformWhitelist
}

// GetStablePlatforms returns only stable platforms.
func GetStablePlatforms() map[string]SupportedPlatform {
	stable := make(map[string]SupportedPlatform)
	for name, platform := range PlatformWhitelist {
		if platform.Stable {
			stable[name] = platform
		}
	}
	return stable
}

// IsSupportedPlatform checks if a platform is in the whitelist.
func IsSupportedPlatform(platform string) bool {
	_, exists := PlatformWhitelist[platform]
	return exists
}

// IsStablePlatform checks if a platform is stable.
func IsStablePlatform(platform string) bool {
	p, exists := PlatformWhitelist[platform]
	return exists && p.Stable
}

// ValidatePlatformRecommendation validates an architect's platform recommendation.
func ValidatePlatformRecommendation(rec *PlatformRecommendation) error {
	if rec.Platform == "" {
		return fmt.Errorf("platform is required")
	}

	// Check if platform is supported
	if !IsSupportedPlatform(rec.Platform) {
		return fmt.Errorf("platform '%s' is not supported", rec.Platform)
	}

	// Check confidence threshold
	if rec.Confidence < 0.0 || rec.Confidence > 1.0 {
		return fmt.Errorf("confidence must be between 0.0 and 1.0, got %f", rec.Confidence)
	}

	// For multi-stack projects, validate all platforms
	if rec.MultiStack {
		if len(rec.Platforms) == 0 {
			return fmt.Errorf("multi-stack project must specify platforms")
		}

		for _, platform := range rec.Platforms {
			if !IsSupportedPlatform(platform) {
				return fmt.Errorf("platform '%s' in multi-stack is not supported", platform)
			}
		}
	}

	return nil
}

// ScorePlatformKeywords scores how well a platform matches keywords in text.
func ScorePlatformKeywords(platform, text string) float64 {
	p, exists := PlatformWhitelist[platform]
	if !exists {
		return 0.0
	}

	text = strings.ToLower(text)
	score := 0.0
	keywordCount := 0

	for _, keyword := range p.Keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			score += 1.0
			keywordCount++
		}
	}

	// Normalize score based on keyword count and platform base confidence
	if keywordCount > 0 {
		normalizedScore := score / float64(len(p.Keywords))
		return normalizedScore * p.Confidence
	}

	return 0.0
}

// RecommendPlatformsFromText analyzes text and recommends platforms based on keywords.
func RecommendPlatformsFromText(text string) []PlatformRecommendation {
	var recommendations []PlatformRecommendation

	// Score all stable platforms
	for name, platform := range GetStablePlatforms() {
		score := ScorePlatformKeywords(name, text)
		if score > 0.1 { // Only include platforms with some confidence
			recommendations = append(recommendations, PlatformRecommendation{
				Platform:   name,
				Confidence: score,
				Rationale:  fmt.Sprintf("Keywords matched: %v", platform.Keywords),
				MultiStack: false,
				Platforms:  []string{name},
			})
		}
	}

	// Sort by confidence (highest first)
	for i := 0; i < len(recommendations); i++ {
		for j := i + 1; j < len(recommendations); j++ {
			if recommendations[i].Confidence < recommendations[j].Confidence {
				recommendations[i], recommendations[j] = recommendations[j], recommendations[i]
			}
		}
	}

	return recommendations
}

// GetDefaultPlatform returns the default platform for low-confidence situations.
func GetDefaultPlatform() string {
	return "null"
}

// RequiresHumanApproval determines if a platform recommendation requires human approval.
func RequiresHumanApproval(rec *PlatformRecommendation) bool {
	// Require approval for low confidence
	if rec.Confidence < 0.3 {
		return true
	}

	// Require approval for unstable platforms
	if !IsStablePlatform(rec.Platform) {
		return true
	}

	// Require approval for multi-stack projects with exotic combinations
	if rec.MultiStack && len(rec.Platforms) > 2 {
		return true
	}

	return false
}

// GetPlatformDisplayName returns the human-readable name for a platform.
func GetPlatformDisplayName(platform string) string {
	if p, exists := PlatformWhitelist[platform]; exists {
		return p.DisplayName
	}
	return platform
}
