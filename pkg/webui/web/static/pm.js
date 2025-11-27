// PM Agent UI Controller
// Manages PM interview, preview, and submission workflows

class PMController {
    constructor() {
        this.currentTab = 'interview';
        this.sessionID = null;
        this.messageCount = 0;
        this.pmState = 'UNKNOWN';
        this.previewLoaded = false; // Track if preview has been loaded
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

        // Upload tab
        const uploadArea = document.getElementById('upload-area');
        const fileInput = document.getElementById('pm-spec-file');

        uploadArea.addEventListener('click', () => fileInput.click());
        uploadArea.addEventListener('dragover', (e) => {
            e.preventDefault();
            uploadArea.classList.add('border-maestro-blue', 'bg-blue-50');
        });
        uploadArea.addEventListener('dragleave', () => {
            uploadArea.classList.remove('border-maestro-blue', 'bg-blue-50');
        });
        uploadArea.addEventListener('drop', (e) => {
            e.preventDefault();
            uploadArea.classList.remove('border-maestro-blue', 'bg-blue-50');
            const file = e.dataTransfer.files[0];
            if (file) this.uploadSpec(file);
        });
        fileInput.addEventListener('change', (e) => {
            const file = e.target.files[0];
            if (file) this.uploadSpec(file);
        });

        // Interview tab
        document.getElementById('start-interview-btn').addEventListener('click', () => this.startInterview());
        document.getElementById('interview-send-btn').addEventListener('click', () => this.sendInterviewMessage());
        document.getElementById('interview-input').addEventListener('keypress', (e) => {
            if (e.key === 'Enter') this.sendInterviewMessage();
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
        switch (status.state) {
            case 'WAITING':
                badge.classList.add('bg-green-100', 'text-green-800');
                break;
            case 'WORKING':
                badge.classList.add('bg-blue-100', 'text-blue-800');
                // Auto-switch to interview tab when PM transitions from upload to WORKING for bootstrap questions
                if (this.currentTab === 'upload' && status.has_session) {
                    this.switchTab('interview');
                    // Show chat interface (session is already started by upload)
                    document.getElementById('interview-start-section').classList.add('hidden');
                    document.getElementById('interview-chat-section').classList.remove('hidden');

                    // Set session ID so maestro.js knows there's an active session
                    // Use a pseudo-session ID for spec upload workflow
                    if (!window.maestroUI.pmSessionId) {
                        window.maestroUI.pmSessionId = `pm_upload_${Date.now()}`;
                    }
                }
                break;
            case 'AWAIT_USER':
                badge.classList.add('bg-blue-100', 'text-blue-800');
                // Always switch to interview tab when PM is awaiting user response
                // This handles both initial interviews and architect feedback cycles
                if (this.currentTab !== 'interview') {
                    this.switchTab('interview');
                    // Ensure chat interface is visible if session exists
                    if (status.has_session) {
                        document.getElementById('interview-start-section').classList.add('hidden');
                        document.getElementById('interview-chat-section').classList.remove('hidden');
                    }
                }
                break;
            case 'PREVIEW':
                badge.classList.add('bg-yellow-100', 'text-yellow-800');
                // Auto-switch to preview tab and load spec (only once)
                if (this.currentTab !== 'preview') {
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
        if (status.has_session) {
            this.sessionID = status.session_id;
            this.messageCount = status.message_count;
        }
    }

    async uploadSpec(file) {
        if (!file.name.endsWith('.md')) {
            this.showUploadStatus('Only .md files are allowed', 'error');
            return;
        }

        if (file.size > 100 * 1024) {
            this.showUploadStatus('File too large (max 100KB)', 'error');
            return;
        }

        // Allow upload in WAITING (before interview) and AWAIT_USER (during interview)
        // Block upload in WORKING (actively processing)
        if (this.pmState !== 'WAITING' && this.pmState !== 'AWAIT_USER') {
            this.showUploadStatus(`PM is busy (state: ${this.pmState})`, 'error');
            return;
        }

        const formData = new FormData();
        formData.append('file', file);

        try {
            this.showUploadStatus('Uploading...', 'loading');

            const response = await fetch('/api/pm/upload', {
                method: 'POST',
                body: formData
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            const result = await response.json();
            this.showUploadStatus(result.message, 'success');

            // Clear file input
            document.getElementById('pm-spec-file').value = '';
        } catch (error) {
            this.showUploadStatus(`Upload failed: ${error.message}`, 'error');
        }
    }

    showUploadStatus(message, type) {
        const statusDiv = document.getElementById('upload-status');
        const statusText = document.getElementById('upload-status-text');

        statusDiv.classList.remove('hidden', 'bg-green-100', 'border-green-300', 'bg-red-100', 'border-red-300', 'bg-blue-100', 'border-blue-300');
        statusText.classList.remove('text-green-800', 'text-red-800', 'text-blue-800');

        statusText.textContent = message;
        statusDiv.classList.remove('hidden');

        if (type === 'success') {
            statusDiv.classList.add('bg-green-100', 'border-green-300');
            statusText.classList.add('text-green-800');
        } else if (type === 'error') {
            statusDiv.classList.add('bg-red-100', 'border-red-300');
            statusText.classList.add('text-red-800');
        } else if (type === 'loading') {
            statusDiv.classList.add('bg-blue-100', 'border-blue-300');
            statusText.classList.add('text-blue-800');
        }
    }

    async startInterview() {
        const expertise = document.getElementById('expertise-level').value;

        try {
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
        }
    }

    async sendInterviewMessage() {
        const input = document.getElementById('interview-input');
        const message = input.value.trim();

        if (!message || !this.sessionID) return;

        try {
            // Clear input immediately
            input.value = '';

            // Post message to chat API - maestro.js polling will display it
            const response = await fetch('/api/pm/chat', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    session_id: this.sessionID,
                    message: message
                })
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
