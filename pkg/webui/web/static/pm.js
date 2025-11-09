// PM Agent UI Controller
// Manages PM interview, preview, and submission workflows

class PMController {
    constructor() {
        this.currentTab = 'upload';
        this.sessionID = null;
        this.messageCount = 0;
        this.pmState = 'UNKNOWN';
        this.initializeEventListeners();
        this.startStatusPolling();
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
        document.getElementById('interview-done-btn').addEventListener('click', () => this.finishInterview());

        // Preview tab
        document.getElementById('generate-preview-btn').addEventListener('click', () => this.generatePreview());
        document.getElementById('submit-spec-btn').addEventListener('click', () => this.submitSpec());
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
        this.pmState = status.state;

        // Update status badge
        const badge = document.getElementById('pm-status-badge');
        badge.textContent = status.state;

        // Update badge color based on state
        badge.className = 'px-3 py-1 rounded-full text-sm font-medium';
        switch (status.state) {
            case 'WAITING':
                badge.classList.add('bg-green-100', 'text-green-800');
                break;
            case 'INTERVIEWING':
                badge.classList.add('bg-blue-100', 'text-blue-800');
                break;
            case 'DRAFTING':
                badge.classList.add('bg-yellow-100', 'text-yellow-800');
                break;
            case 'SUBMITTING':
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

        if (this.pmState !== 'WAITING') {
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
            // Add user message to UI immediately
            this.addInterviewMessage('user', message);
            input.value = '';

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

            const result = await response.json();
            this.addInterviewMessage('pm', result.reply);
            this.messageCount++;
            document.getElementById('interview-message-count').textContent = `${this.messageCount} messages`;
        } catch (error) {
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

    finishInterview() {
        // TODO: Implement finish interview flow
        // For now, just show the preview tab
        this.switchTab('preview');
    }

    async generatePreview() {
        if (!this.sessionID) {
            this.showPreviewError('No active interview session');
            return;
        }

        try {
            document.getElementById('generate-preview-btn').classList.add('hidden');
            document.getElementById('preview-loading').classList.remove('hidden');
            document.getElementById('preview-error').classList.add('hidden');

            const response = await fetch(`/api/pm/preview?session_id=${this.sessionID}`);

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            const result = await response.json();

            // Render markdown preview
            const previewDiv = document.getElementById('preview-markdown');
            previewDiv.textContent = result.markdown; // TODO: Use markdown renderer

            document.getElementById('preview-loading').classList.add('hidden');
            document.getElementById('preview-container').classList.remove('hidden');
        } catch (error) {
            document.getElementById('preview-loading').classList.add('hidden');
            this.showPreviewError(error.message);
        }
    }

    showPreviewError(message) {
        const errorDiv = document.getElementById('preview-error');
        const errorText = document.getElementById('preview-error-text');
        errorText.textContent = message;
        errorDiv.classList.remove('hidden');
    }

    async submitSpec() {
        if (!this.sessionID) {
            alert('No spec available to submit');
            return;
        }

        if (!confirm('Submit this specification to the architect for review?')) {
            return;
        }

        try {
            const response = await fetch('/api/pm/submit', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ session_id: this.sessionID })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            const result = await response.json();

            if (result.success) {
                alert('Specification submitted successfully!');
                // Reset UI
                this.sessionID = null;
                this.messageCount = 0;
                this.switchTab('upload');
                document.getElementById('interview-start-section').classList.remove('hidden');
                document.getElementById('interview-chat-section').classList.add('hidden');
                document.getElementById('preview-container').classList.add('hidden');
                document.getElementById('generate-preview-btn').classList.remove('hidden');
            } else {
                alert(`Submission failed: ${result.message}\n\nErrors:\n${result.errors.join('\n')}`);
            }
        } catch (error) {
            alert(`Failed to submit spec: ${error.message}`);
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
