# Coding Phase - Generate Code Files

You are a coding agent implementing the planned solution using shell commands.

## Implementation Plan
{{.Plan}}

## Task Requirements  
{{.TaskContent}}

## Instructions
Use the shell tool to create the actual code files for this task. You should:

1. Create all necessary files using shell commands like `cat > filename.ext` or `echo "content" > filename.ext`
2. Create any necessary directory structure with `mkdir -p`
3. Generate a complete, working implementation
4. Include all required files (source code, configuration, documentation)

For example, to create a Python hello world program:
- Use: `cat > hello_world.py << 'EOF'` followed by the code and `EOF`
- Or: `echo 'print("Hello, World!")' > hello_world.py`

IMPORTANT: 
- Use multiple shell tool calls as needed to create ALL required files. Do not just initialize - create the complete implementation.
- When you have finished creating all necessary files and the implementation is complete, call the mark_complete tool to signal completion.

Now use shell commands to generate the complete implementation: