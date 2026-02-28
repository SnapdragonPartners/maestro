// PM Agent UI Controller
// Manages PM interview, preview, and submission workflows

class PMController {
    constructor() {
        this.currentTab = 'interview';
        this.sessionID = null;
        this.messageCount = 0;
        this.pmState = 'UNKNOWN';
        this.previewLoaded = false; // Track if preview has been loaded
        this.attachedFile = null; // Currently attached file metadata
        this.attachedFileContent = null; // File content as text
        this.initializeEventListeners();
        this.startStatusPolling();
        // Activate interview tab on load
        this.switchTab('interview');
    }

    initializeEventListeners() {
        // Tab switching
        document.querySelectorAll('.pm-tab').forEach(tab => {
            tab.addEventListener('click', (e) => this.switchTab(e.target.id.replace('tab-', '')));
        });

        // Interview tab
        document.getElementById('start-interview-btn').addEventListener('click', () => this.startInterview());
        document.getElementById('interview-send-btn').addEventListener('click', () => this.sendInterviewMessage());

        // Textarea: Enter sends, Shift+Enter inserts newline
        const textarea = document.getElementById('interview-input');
        textarea.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                this.sendInterviewMessage();
            }
        });

        // Auto-expand textarea on input (up to ~6 rows)
        textarea.addEventListener('input', () => {
            textarea.style.height = 'auto';
            const maxHeight = 6 * 24; // ~6 rows at ~24px line height
            textarea.style.height = Math.min(textarea.scrollHeight, maxHeight) + 'px';
        });

        // File upload button (paperclip)
        document.getElementById('interview-file-btn').addEventListener('click', () => {
            document.getElementById('interview-file-input').click();
        });

        // File input change handler
        document.getElementById('interview-file-input').addEventListener('change', (e) => {
            const file = e.target.files[0];
            if (file) this.attachFile(file);
            // Reset input so the same file can be re-selected
            e.target.value = '';
        });

        // Remove attachment button
        document.getElementById('interview-attachment-remove').addEventListener('click', () => {
            this.clearAttachment();
        });

        // Drag-and-drop on the chat area
        const chatSection = document.getElementById('interview-chat-section');
        chatSection.addEventListener('dragover', (e) => {
            e.preventDefault();
            chatSection.classList.add('ring-2', 'ring-maestro-blue', 'ring-opacity-50');
        });
        chatSection.addEventListener('dragleave', () => {
            chatSection.classList.remove('ring-2', 'ring-maestro-blue', 'ring-opacity-50');
        });
        chatSection.addEventListener('drop', (e) => {
            e.preventDefault();
            chatSection.classList.remove('ring-2', 'ring-maestro-blue', 'ring-opacity-50');
            const file = e.dataTransfer.files[0];
            if (file) this.attachFile(file);
        });

        // Preview tab
        document.getElementById('continue-interview-btn').addEventListener('click', () => this.continueInterview());
        document.getElementById('submit-to-architect-btn').addEventListener('click', () => this.submitToArchitect());
    }

    switchTab(tabName) {
        // Update tab styling
        document.querySelectorAll('.pm-tab').forEach(tab => {
            tab.classList.remove('active', 'border-maestro-blue', 'text-maestro-blue');
            tab.classList.add('border-transparent', 'text-gray-500');
        });
        const activeTab = document.getElementById(`tab-${tabName}`);
        activeTab.classList.add('active', 'border-maestro-blue', 'text-maestro-blue');
        activeTab.classList.remove('border-transparent', 'text-gray-500');

        // Update content visibility
        document.querySelectorAll('.pm-tab-content').forEach(content => {
            content.classList.add('hidden');
        });
        document.getElementById(`pm-${tabName}-content`).classList.remove('hidden');

        this.currentTab = tabName;
    }

    async startStatusPolling() {
        // Poll PM status every 2 seconds
        setInterval(async () => {
            try {
                const response = await fetch('/api/pm/status');
                if (!response.ok) return;

                const status = await response.json();
                this.updatePMStatus(status);
            } catch (error) {
                console.error('Failed to fetch PM status:', error);
            }
        }, 2000);
    }

    updatePMStatus(status) {
        const previousState = this.pmState;
        this.pmState = status.state;

        // Reset previewLoaded flag when leaving PREVIEW state
        if (previousState === 'PREVIEW' && status.state !== 'PREVIEW') {
            this.previewLoaded = false;
        }

        // Update status badge
        const badge = document.getElementById('pm-status-badge');
        badge.textContent = status.state;

        // Update badge color based on state
        badge.className = 'px-3 py-1 rounded-full text-sm font-medium';

        // Skip auto-tab-switching if user is intentionally viewing the demo tab
        // This allows users to check demo status while PM is working
        const skipAutoSwitch = (this.currentTab === 'demo');

        switch (status.state) {
            case 'WAITING':
                badge.classList.add('bg-green-100', 'text-green-800');
                break;
            case 'WORKING':
                badge.classList.add('bg-blue-100', 'text-blue-800');
                // Ensure chat interface is visible when PM is working with a session
                if (!skipAutoSwitch && status.has_session && this.currentTab === 'interview') {
                    document.getElementById('interview-start-section').classList.add('hidden');
                    document.getElementById('interview-chat-section').classList.remove('hidden');
                    if (!this.sessionID) {
                        this.sessionID = `pm_session_${Date.now()}`;
                        window.maestroUI.pmSessionId = this.sessionID;
                    }
                }
                break;
            case 'AWAIT_USER':
                badge.classList.add('bg-blue-100', 'text-blue-800');
                // Auto-switch to interview tab when PM is awaiting user response
                // This handles both initial interviews and architect feedback cycles
                // But respect user's demo tab selection
                if (!skipAutoSwitch && this.currentTab !== 'interview') {
                    this.switchTab('interview');
                }
                // Ensure chat interface is visible and session ID is set if session exists
                // This handles page refreshes when PM is already in AWAIT_USER state
                if (status.has_session) {
                    document.getElementById('interview-start-section').classList.add('hidden');
                    document.getElementById('interview-chat-section').classList.remove('hidden');
                    // Ensure we have a session ID for the chat (handles page refresh)
                    if (!this.sessionID) {
                        this.sessionID = `pm_session_${Date.now()}`;
                        window.maestroUI.pmSessionId = this.sessionID;
                    }
                }
                break;
            case 'PREVIEW':
                badge.classList.add('bg-yellow-100', 'text-yellow-800');
                // Auto-switch to preview tab and load spec (only once)
                // But respect user's demo tab selection
                if (!skipAutoSwitch && this.currentTab !== 'preview') {
                    this.switchTab('preview');
                }
                // Load the spec only once when entering PREVIEW state
                if (!this.previewLoaded) {
                    this.previewLoaded = true;
                    this.loadPreviewSpec();
                }
                break;
            case 'AWAIT_ARCHITECT':
                badge.classList.add('bg-purple-100', 'text-purple-800');
                break;
            case 'ERROR':
                badge.classList.add('bg-red-100', 'text-red-800');
                break;
            default:
                badge.classList.add('bg-gray-100', 'text-gray-600');
        }

        // Show/hide availability banner
        const banner = document.getElementById('pm-not-ready-banner');
        if (status.state === 'UNKNOWN' || status.state === 'ERROR') {
            banner.classList.remove('hidden');
        } else {
            banner.classList.add('hidden');
        }

        // Update interview session info
        // Only update session ID from status if it's present (avoid overwriting valid ID with empty)
        if (status.has_session) {
            if (status.session_id) {
                this.sessionID = status.session_id;
            }
            this.messageCount = status.message_count;
        }
    }

    attachFile(file) {
        // Validate .md extension
        if (!file.name.endsWith('.md')) {
            alert('Only .md files are supported.');
            return;
        }

        // Validate size (100KB max)
        if (file.size > 100 * 1024) {
            alert('File too large (max 100KB).');
            return;
        }

        // Read file content
        const reader = new FileReader();
        reader.onload = (e) => {
            this.attachedFile = { name: file.name, size: file.size };
            this.attachedFileContent = e.target.result;
            this.showAttachment(file.name, file.size);
        };
        reader.onerror = () => {
            this.attachedFile = null;
            this.attachedFileContent = null;
            alert('Failed to read file. Please try again.');
        };
        reader.readAsText(file);
    }

    showAttachment(name, size) {
        const sizeStr = size < 1024 ? `${size} B` : `${(size / 1024).toFixed(1)} KB`;
        document.getElementById('interview-attachment-name').textContent = name;
        document.getElementById('interview-attachment-size').textContent = `(${sizeStr})`;
        document.getElementById('interview-attachment').classList.remove('hidden');
    }

    clearAttachment() {
        this.attachedFile = null;
        this.attachedFileContent = null;
        document.getElementById('interview-attachment').classList.add('hidden');
    }

    async startInterview() {
        const expertise = document.getElementById('expertise-level').value;
        const btn = document.getElementById('start-interview-btn');

        try {
            // Disable button and show spinner during the POST
            btn.disabled = true;
            btn.textContent = 'Starting...';

            const response = await fetch('/api/pm/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ expertise })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            const result = await response.json();
            this.sessionID = result.session_id;

            // Show chat interface
            document.getElementById('interview-start-section').classList.add('hidden');
            document.getElementById('interview-chat-section').classList.remove('hidden');

            this.addInterviewMessage('system', result.message);
        } catch (error) {
            alert(`Failed to start interview: ${error.message}`);
        } finally {
            btn.disabled = false;
            btn.textContent = 'Start Interview';
        }
    }

    async sendInterviewMessage() {
        const textarea = document.getElementById('interview-input');
        const message = textarea.value.trim();
        const hasFile = this.attachedFile !== null;

        // Need either a message or a file
        if (!message && !hasFile) return;
        if (!this.sessionID) {
            console.log('[PM] sendInterviewMessage blocked: no session');
            return;
        }

        // Default message when only a file is attached
        const textToSend = message || "I've uploaded a specification file.";

        console.log('[PM] sendInterviewMessage sending:', {
            sessionID: this.sessionID,
            message: textToSend.substring(0, 50),
            hasFile: hasFile
        });

        try {
            // Clear input immediately
            textarea.value = '';
            textarea.style.height = 'auto';

            // Build request body
            const body = {
                session_id: this.sessionID,
                message: textToSend
            };
            if (hasFile) {
                body.file_content = this.attachedFileContent;
                body.file_name = this.attachedFile.name;
            }

            // Clear attachment after capturing content
            if (hasFile) this.clearAttachment();

            // Post message to chat API - maestro.js polling will display it
            const response = await fetch('/api/pm/chat', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body)
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            // Update message count (maestro.js polling handles display)
            this.messageCount++;
            document.getElementById('interview-message-count').textContent = `${this.messageCount} messages`;
        } catch (error) {
            // Show error messages inline (not through chat system)
            this.addInterviewMessage('system', `Error: ${error.message}`);
        }
    }

    addInterviewMessage(sender, text) {
        const messagesDiv = document.getElementById('interview-messages');
        const messageDiv = document.createElement('div');
        messageDiv.className = 'p-3 rounded-lg';

        if (sender === 'user') {
            messageDiv.classList.add('bg-blue-100', 'ml-8');
            messageDiv.innerHTML = `<p class="text-sm text-blue-900"><strong>You:</strong> ${this.escapeHtml(text)}</p>`;
        } else if (sender === 'pm') {
            messageDiv.classList.add('bg-gray-100', 'mr-8');
            messageDiv.innerHTML = `<p class="text-sm text-gray-900"><strong>PM:</strong> ${this.escapeHtml(text)}</p>`;
        } else {
            messageDiv.classList.add('bg-yellow-50', 'border', 'border-yellow-200');
            messageDiv.innerHTML = `<p class="text-sm text-yellow-900">${this.escapeHtml(text)}</p>`;
        }

        messagesDiv.appendChild(messageDiv);
        messagesDiv.scrollTop = messagesDiv.scrollHeight;
    }


    showPreviewError(message) {
        const errorDiv = document.getElementById('preview-error');
        const errorText = document.getElementById('preview-error-text');
        errorText.textContent = message;
        errorDiv.classList.remove('hidden');
    }

    async loadPreviewSpec() {
        try {
            // Show loading state
            document.getElementById('preview-error').classList.add('hidden');
            document.getElementById('preview-loading').classList.remove('hidden');

            // Use sessionID if available, otherwise use a placeholder since we always query pm-001
            const sessionParam = this.sessionID || 'current';
            const response = await fetch(`/api/pm/preview/spec?session_id=${sessionParam}`);

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            const result = await response.json();

            // Render markdown preview
            const previewDiv = document.getElementById('preview-markdown');

            // Display the spec markdown as preformatted text
            // TODO: Use a proper markdown renderer library for better formatting
            if (result.markdown && result.markdown.length > 0) {
                // Wrap in a pre tag to preserve formatting
                previewDiv.innerHTML = `<pre class="whitespace-pre-wrap text-sm">${this.escapeHtml(result.markdown)}</pre>`;
            } else {
                // No spec available
                previewDiv.innerHTML = '<div class="bg-yellow-50 border border-yellow-200 rounded-lg p-4"><p class="text-yellow-800"><strong>⏳ Specification Not Ready</strong></p><p class="text-sm text-yellow-600 mt-2">The specification is still being generated. Please wait...</p></div>';
            }

            // Show preview container with action buttons
            document.getElementById('preview-loading').classList.add('hidden');
            document.getElementById('preview-container').classList.remove('hidden');
            document.getElementById('preview-actions').classList.remove('hidden');
        } catch (error) {
            document.getElementById('preview-loading').classList.add('hidden');
            this.showPreviewError(error.message);
        }
    }

    async continueInterview() {
        try {
            const sessionParam = this.sessionID || 'current';
            const response = await fetch('/api/pm/preview/action', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    session_id: sessionParam,
                    action: 'continue_interview'
                })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            // Switch back to interview tab
            this.switchTab('interview');
        } catch (error) {
            alert(`Failed to continue interview: ${error.message}`);
        }
    }

    async submitToArchitect() {
        if (!confirm('Submit this specification to the architect for review and implementation?')) {
            return;
        }

        try {
            const sessionParam = this.sessionID || 'current';
            const response = await fetch('/api/pm/preview/action', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    session_id: sessionParam,
                    action: 'submit_to_architect'
                })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            // Success - UI will update via polling to show AWAIT_ARCHITECT state

            // Hide action buttons during architect review
            document.getElementById('preview-actions').classList.add('hidden');
        } catch (error) {
            alert(`Failed to submit to architect: ${error.message}`);
        }
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Initialize PM controller when page loads
let pmController;
document.addEventListener('DOMContentLoaded', () => {
    pmController = new PMController();
});
