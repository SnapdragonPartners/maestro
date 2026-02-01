# Story Generation from Approved Specification

You have reviewed and approved the following specification. Now generate implementation stories that break down the requirements into implementable tasks.

## Approved Specification

```
{{.TaskContent}}
```
{{if .Extra.knowledge_context}}
## Architectural Knowledge

The following architectural patterns, rules, and standards are established for this project:

```dot
{{.Extra.knowledge_context}}
```

**IMPORTANT**: When generating requirements, ensure they align with these established architectural patterns, rules, and standards. Consider:
- Existing patterns that should be followed
- Rules that must be adhered to (especially high/critical priority)
- Standards for API design, testing, security, etc.
- Current vs deprecated approaches
{{end}}

## Your Task

Generate implementation stories from this approved specification. You MUST call the `submit_stories` tool with your generated stories.

You do not have access to exploration tools in this phase - story generation should be based solely on the approved specification content.

## Story Generation Guidelines

**CRITICAL**: First identify the platform/language specified in the specification. Look for explicit declarations like "Platform: Go", "Platform: Python", etc.

**PLATFORM CONSISTENCY RULE**: All requirements, examples, tools, and implementation details MUST be consistent with the identified platform. Do not mix platforms or suggest tools from different languages.

Extract discrete, implementable requirements from the specification:

For each requirement you identify:

1. **Extract clear, actionable tasks** from the content
2. **Write detailed descriptions** that clarify the intent
3. **Generate comprehensive acceptance criteria** (3-5 criteria per requirement)
4. **Estimate complexity** using 1-5 points:
   - 1 point: Simple changes, basic endpoints
   - 2 points: Standard features, simple integrations
   - 3 points: Complex features, database changes
   - 4 points: Major integrations, security features
   - 5 points: Complex systems, architectural changes
5. **Identify logical dependencies** between requirements (use requirement titles)
   - **For app stories**: Scope stories to minimize overlap and enable parallel development - stories should touch different files/components where possible to avoid merge conflicts
   - **For devops stories**: Dependencies are fine as infrastructure work is typically sequential
6. **Classify story type** as either:
   - **"devops"**: Infrastructure, containers, deployment, configuration - minimally scoped to infrastructure tasks ONLY
   - **"app"**: Application code, features, business logic, algorithms, data processing
   - **Default to "app"** when uncertain - app containers provide full development environments

## Output Format

When you have completed your analysis, you MUST call the `submit_stories` tool with the following parameters:

- **analysis**: Brief summary of what you found in the specification and the identified platform (e.g., 'Go-based project requiring module setup and linting configuration')
- **platform**: The identified platform (e.g., "go", "python", "nodejs")
- **requirements**: Array of requirement objects, each containing:
  - **title**: Concise, clear requirement title
  - **description**: Detailed description of what needs to be implemented using platform-appropriate tools and examples
  - **acceptance_criteria**: Array of 3-5 specific, testable criteria (platform-consistent)
  - **dependencies**: Array of requirement titles this depends on
  - **story_type**: Either "app" (application code) or "devops" (infrastructure)

**Important Guidelines:**
- **MAINTAIN PLATFORM CONSISTENCY**: Use only tools and approaches appropriate for the identified platform. Do not mix tools or concepts from different programming languages
- **OPTIMIZE FOR PARALLEL DEVELOPMENT**: For app stories, scope stories to minimize overlap in files and components. This enables multiple coding agents to work in parallel without merge conflicts
- Focus on implementable features, not documentation or planning tasks
- Make requirements specific enough for a coding agent to implement
- Ensure acceptance criteria are testable and concrete
- Dependencies should reference other requirement titles in this analysis
- If the spec is unclear, make reasonable assumptions and note them in the description
- Extract value even from poorly formatted or incomplete specifications

**Story Classification Guidelines:**
- **DevOps stories** should be minimally scoped to pure infrastructure tasks that don't require language-specific toolchains
- **DevOps examples**: Raw Docker container building/copying, Dockerfile creation, deployment scripts, CI/CD pipeline setup, infrastructure configuration, container registry operations
- **App examples**: Language module setup (go.mod for Go, package.json for Node.js, requirements.txt for Python), build system configuration (Makefiles, build.gradle), linting setup (golangci-lint for Go, eslint for JavaScript, ruff for Python), language-specific tools, feature implementation, bug fixes, algorithm development, API endpoints, business logic, data processing
- **Key distinction**: If a task requires language-specific knowledge or toolchain (Go, Node.js, Python, etc.), classify as "app"
- **When in doubt, classify as "app"** since app containers provide full development environments with language toolchains

**External Services (Databases, Caches, etc.):**
When a story requires external services like databases (PostgreSQL, MySQL), caches (Redis, Memcached), message queues (RabbitMQ, Kafka), or other infrastructure:
- **Include in story description**: Explicitly state that the story should use Docker Compose for external services
- **Specify the compose file location**: Services should be defined in `.maestro/compose.yml`
- **Use the compose_up tool**: Instruct coders to use the `compose_up` tool to start required services
- **Example guidance in story**: "Use Docker Compose (`.maestro/compose.yml`) to run PostgreSQL. Call `compose_up` to start the database before running tests."
- **Do NOT assume services are pre-running**: Each story that needs services must explicitly manage them via compose