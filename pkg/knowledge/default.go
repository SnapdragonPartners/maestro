package knowledge

// DefaultKnowledgeGraph returns the default knowledge graph for new projects.
const DefaultKnowledgeGraph = `digraph ProjectKnowledge {
    // Core architectural patterns
    "error-handling" [
        type="pattern"
        level="implementation"
        status="current"
        description="Wrap errors with context using fmt.Errorf with %w"
        example="return fmt.Errorf(\"failed to fetch user: %w\", err)"
    ];

    "api-standards" [
        type="rule"
        level="architecture"
        status="current"
        description="REST APIs follow OpenAPI 3.0 specification"
        priority="high"
    ];

    "test-coverage" [
        type="rule"
        level="implementation"
        status="current"
        description="Maintain minimum 80% test coverage for new code"
        priority="critical"
    ];

    "code-style" [
        type="pattern"
        level="implementation"
        status="current"
        description="Follow language-specific style guides (gofmt, prettier, eslint, etc.)"
    ];

    "logging-standards" [
        type="pattern"
        level="implementation"
        status="current"
        description="Use structured logging with appropriate log levels"
        example="logger.Info(\"user created\", \"user_id\", userID)"
    ];

    "security-headers" [
        type="rule"
        level="architecture"
        status="current"
        description="All HTTP responses must include security headers (CSP, X-Frame-Options, etc.)"
        priority="critical"
    ];
}
`
