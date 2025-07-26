// Maestro Web UI JavaScript
class MaestroUI {
    constructor() {
        this.pollingInterval = 1000; // 1 second
        this.failureCount = 0;
        this.maxFailures = 3;
        this.isConnected = true;
        this.autoscroll = true;
        this.queuePollingIntervals = {};
        
        this.init();
    }

    init() {
        this.setupEventListeners();
        this.startPolling();
        this.updateLastUpdated();
    }

    setupEventListeners() {
        // Upload area
        const uploadArea = document.getElementById('upload-area');
        const fileInput = document.getElementById('spec-file');
        
        uploadArea.addEventListener('click', () => fileInput.click());
        uploadArea.addEventListener('dragover', this.handleDragOver.bind(this));
        uploadArea.addEventListener('drop', this.handleDrop.bind(this));
        fileInput.addEventListener('change', this.handleFileSelect.bind(this));

        // Control buttons
        document.getElementById('cancel-run').addEventListener('click', this.cancelRun.bind(this));
        document.getElementById('refresh-data').addEventListener('click', this.refreshData.bind(this));
        document.getElementById('show-escalations').addEventListener('click', this.showEscalations.bind(this));
        document.getElementById('close-modal').addEventListener('click', this.closeModal.bind(this));
        
        // Log controls
        document.getElementById('log-domain').addEventListener('change', this.onLogDomainChange.bind(this));
        document.getElementById('autoscroll').addEventListener('change', this.onAutoscrollChange.bind(this));
        document.getElementById('clear-logs').addEventListener('click', this.clearLogs.bind(this));
    }

    async startPolling() {
        this.pollAgents();
        this.pollStories();
        this.pollLogs();
        setInterval(() => this.pollAgents(), this.pollingInterval);
        setInterval(() => this.pollStories(), this.pollingInterval);
        setInterval(() => this.pollLogs(), this.pollingInterval);
        setInterval(() => this.updateLastUpdated(), 1000);
    }

    async pollAgents() {
        try {
            const response = await fetch('/api/agents');
            if (!response.ok) throw new Error('Failed to fetch agents');
            
            const agents = await response.json();
            this.updateAgentGrid(agents);
            this.checkEscalations(agents);
            this.setConnectionStatus(true);
            
        } catch (error) {
            console.error('Error polling agents:', error);
            this.handleConnectionError();
        }
    }

    async pollStories() {
        try {
            const response = await fetch('/api/stories');
            if (!response.ok) throw new Error('Failed to fetch stories');
            
            const stories = await response.json();
            this.updateStories(stories);
            this.setConnectionStatus(true);
            
        } catch (error) {
            console.error('Error polling stories:', error);
            this.handleConnectionError();
        }
    }

    async pollLogs() {
        try {
            const domain = document.getElementById('log-domain').value;
            const url = domain ? `/api/logs?domain=${encodeURIComponent(domain)}` : '/api/logs';
            
            const response = await fetch(url);
            if (!response.ok) throw new Error('Failed to fetch logs');
            
            const logs = await response.json();
            this.updateLogs(logs);
            
        } catch (error) {
            console.error('Error polling logs:', error);
        }
    }

    updateAgentGrid(agents) {
        const grid = document.getElementById('agent-grid');
        grid.innerHTML = '';

        agents.forEach(agent => {
            const card = this.createAgentCard(agent);
            grid.appendChild(card);
        });
    }

    createAgentCard(agent) {
        const card = document.createElement('div');
        card.className = 'agent-card cursor-pointer';
        card.onclick = () => this.showAgentDetails(agent.id);

        const stateClass = this.getStateClass(agent.state);
        const timeDiff = this.getTimeSince(agent.last_ts);

        card.innerHTML = `
            <div class="flex items-center justify-between mb-2">
                <h3 class="font-medium text-gray-900">${agent.id}</h3>
                <span class="px-2 py-1 text-xs rounded-full ${stateClass}">${agent.state}</span>
            </div>
            <div class="text-sm text-gray-600">
                <p>Role: ${agent.role}</p>
                <p>Last updated: ${timeDiff}</p>
            </div>
        `;

        return card;
    }

    getStateClass(state) {
        const stateMap = {
            'WAITING': 'state-waiting',
            'WORKING': 'state-working',
            'CODING': 'state-working',
            'PLANNING': 'state-working',
            'TESTING': 'state-working',
            'DONE': 'state-done',
            'COMPLETED': 'state-done',
            'ERROR': 'state-error',
            'FAILED': 'state-error',
            'ESCALATED': 'state-escalated'
        };
        return stateMap[state] || 'bg-gray-100 text-gray-800';
    }

    updateStories(stories) {
        const container = document.getElementById('stories-container');
        const loading = document.getElementById('stories-loading');
        const empty = document.getElementById('stories-empty');
        const list = document.getElementById('stories-list');

        // Hide loading state
        loading.classList.add('hidden');

        if (!stories || stories.length === 0) {
            empty.classList.remove('hidden');
            list.classList.add('hidden');
            return;
        }

        empty.classList.add('hidden');
        list.classList.remove('hidden');

        // Sort stories by ID for consistent display
        stories.sort((a, b) => a.id.localeCompare(b.id));

        list.innerHTML = stories.map(story => this.createStoryCard(story)).join('');
    }

    createStoryCard(story) {
        const statusClass = this.getStoryStatusClass(story.status);
        const statusIcon = this.getStoryStatusIcon(story.status);
        const timeInfo = this.getStoryTimeInfo(story);

        return `
            <div class="border border-gray-200 rounded-lg p-4 mb-3">
                <div class="flex items-center justify-between mb-2">
                    <div class="flex items-center space-x-3">
                        <div class="flex-shrink-0">
                            ${statusIcon}
                        </div>
                        <div>
                            <h3 class="font-medium text-gray-900">${story.id}</h3>
                            <p class="text-sm text-gray-600">${story.title || 'Untitled Story'}</p>
                        </div>
                    </div>
                    <span class="px-2 py-1 text-xs rounded-full ${statusClass}">${story.status}</span>
                </div>
                <div class="text-sm text-gray-500 flex items-center justify-between">
                    <div class="flex items-center space-x-4">
                        ${story.estimated_points ? `<span>📊 ${story.estimated_points} pts</span>` : ''}
                        ${story.assigned_agent ? `<span>👤 ${story.assigned_agent}</span>` : ''}
                        ${story.depends_on && story.depends_on.length > 0 ? `<span>🔗 Depends on: ${story.depends_on.join(', ')}</span>` : ''}
                    </div>
                    <div class="text-xs text-gray-400">
                        ${timeInfo}
                    </div>
                </div>
            </div>
        `;
    }

    getStoryStatusClass(status) {
        const statusMap = {
            'pending': 'bg-gray-100 text-gray-800',
            'in_progress': 'bg-blue-100 text-blue-800',
            'waiting_review': 'bg-yellow-100 text-yellow-800',
            'completed': 'bg-green-100 text-green-800',
            'blocked': 'bg-red-100 text-red-800',
            'cancelled': 'bg-gray-100 text-gray-800',
            'await_human_feedback': 'bg-purple-100 text-purple-800'
        };
        return statusMap[status] || 'bg-gray-100 text-gray-800';
    }

    getStoryStatusIcon(status) {
        const iconMap = {
            'pending': '<div class="w-4 h-4 bg-gray-300 rounded-full"></div>',
            'in_progress': '<div class="w-4 h-4 bg-blue-500 rounded-full animate-pulse"></div>',
            'waiting_review': '<div class="w-4 h-4 bg-yellow-500 rounded-full"></div>',
            'completed': '<div class="w-4 h-4 bg-green-500 rounded-full"><svg class="w-3 h-3 text-white ml-0.5 mt-0.5" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"></path></svg></div>',
            'blocked': '<div class="w-4 h-4 bg-red-500 rounded-full flex items-center justify-center"><span class="text-white text-xs">!</span></div>',
            'cancelled': '<div class="w-4 h-4 bg-gray-400 rounded-full flex items-center justify-center"><span class="text-white text-xs">×</span></div>',
            'await_human_feedback': '<div class="w-4 h-4 bg-purple-500 rounded-full flex items-center justify-center"><span class="text-white text-xs">?</span></div>'
        };
        return iconMap[status] || '<div class="w-4 h-4 bg-gray-300 rounded-full"></div>';
    }

    getStoryTimeInfo(story) {
        if (story.completed_at) {
            return `Completed ${this.getTimeSince(story.completed_at)}`;
        }
        if (story.started_at) {
            return `Started ${this.getTimeSince(story.started_at)}`;
        }
        if (story.last_updated) {
            return `Updated ${this.getTimeSince(story.last_updated)}`;
        }
        return '';
    }

    getTimeSince(timestamp) {
        const now = new Date();
        const then = new Date(timestamp);
        const diffMs = now - then;
        const diffSec = Math.floor(diffMs / 1000);
        const diffMin = Math.floor(diffSec / 60);
        const diffHour = Math.floor(diffMin / 60);

        if (diffHour > 0) return `${diffHour}h ago`;
        if (diffMin > 0) return `${diffMin}m ago`;
        return `${diffSec}s ago`;
    }

    async showAgentDetails(agentId) {
        try {
            const response = await fetch(`/api/agent/${encodeURIComponent(agentId)}`);
            if (!response.ok) throw new Error('Failed to fetch agent details');
            
            const agent = await response.json();
            this.showAgentModal(agent);
            
        } catch (error) {
            console.error('Error fetching agent details:', error);
            this.showToast('Error loading agent details', 'error');
        }
    }

    showAgentModal(agent) {
        const modal = document.getElementById('escalation-modal');
        const content = document.getElementById('escalation-content');
        
        content.innerHTML = `
            <div class="space-y-4">
                <div class="grid grid-cols-2 gap-4">
                    <div>
                        <label class="text-sm font-medium text-gray-700">Agent ID</label>
                        <p class="text-sm text-gray-900">${agent.id || 'N/A'}</p>
                    </div>
                    <div>
                        <label class="text-sm font-medium text-gray-700">Current State</label>
                        <p class="text-sm text-gray-900">${agent.state}</p>
                    </div>
                </div>
                
                ${agent.plan ? `
                    <div>
                        <label class="text-sm font-medium text-gray-700">Plan</label>
                        <div class="mt-1 p-3 bg-gray-50 rounded-md">
                            <pre class="text-sm text-gray-900 whitespace-pre-wrap">${agent.plan}</pre>
                        </div>
                    </div>
                ` : ''}
                
                ${agent.task_content ? `
                    <div>
                        <label class="text-sm font-medium text-gray-700">Task Content</label>
                        <div class="mt-1 p-3 bg-gray-50 rounded-md">
                            <pre class="text-sm text-gray-900 whitespace-pre-wrap">${agent.task_content}</pre>
                        </div>
                    </div>
                ` : ''}
                
                ${agent.transitions && agent.transitions.length > 0 ? `
                    <div>
                        <label class="text-sm font-medium text-gray-700">State Transitions</label>
                        <div class="mt-1 overflow-x-auto">
                            <table class="min-w-full divide-y divide-gray-200">
                                <thead class="bg-gray-50">
                                    <tr>
                                        <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 uppercase">From</th>
                                        <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 uppercase">To</th>
                                        <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 uppercase">Time</th>
                                    </tr>
                                </thead>
                                <tbody class="bg-white divide-y divide-gray-200">
                                    ${agent.transitions.map(t => `
                                        <tr>
                                            <td class="px-3 py-2 text-sm text-gray-900">${t.from}</td>
                                            <td class="px-3 py-2 text-sm text-gray-900">${t.to}</td>
                                            <td class="px-3 py-2 text-sm text-gray-500">${new Date(t.ts).toLocaleString()}</td>
                                        </tr>
                                    `).join('')}
                                </tbody>
                            </table>
                        </div>
                    </div>
                ` : ''}
            </div>
        `;
        
        modal.classList.remove('hidden');
    }

    checkEscalations(agents) {
        const escalatedAgents = agents.filter(a => a.state === 'ESCALATED');
        const banner = document.getElementById('escalation-banner');
        const count = document.getElementById('escalation-count');
        
        if (escalatedAgents.length > 0) {
            count.textContent = escalatedAgents.length;
            banner.classList.remove('hidden');
        } else {
            banner.classList.add('hidden');
        }
    }

    updateLogs(logs) {
        const container = document.getElementById('logs-container');
        
        // Clear existing logs
        container.innerHTML = '';
        
        logs.forEach(log => {
            const logLine = document.createElement('div');
            logLine.className = 'mb-1';
            
            const levelColor = {
                'ERROR': 'text-red-400',
                'WARN': 'text-yellow-400',
                'INFO': 'text-blue-400',
                'DEBUG': 'text-gray-400'
            }[log.level] || 'text-green-400';
            
            logLine.innerHTML = `
                <span class="text-gray-500">[${log.timestamp}]</span>
                <span class="text-cyan-400">[${log.agent_id}]</span>
                <span class="${levelColor}">${log.level}:</span>
                <span>${log.message}</span>
            `;
            
            container.appendChild(logLine);
        });
        
        // Auto-scroll if enabled
        if (this.autoscroll) {
            container.scrollTop = container.scrollHeight;
        }
    }

    // File upload handling
    handleDragOver(e) {
        e.preventDefault();
        e.stopPropagation();
        e.currentTarget.classList.add('border-blue-400', 'bg-blue-50');
    }

    handleDrop(e) {
        e.preventDefault();
        e.stopPropagation();
        e.currentTarget.classList.remove('border-blue-400', 'bg-blue-50');
        
        const files = e.dataTransfer.files;
        if (files.length > 0) {
            this.uploadFile(files[0]);
        }
    }

    handleFileSelect(e) {
        const file = e.target.files[0];
        if (file) {
            this.uploadFile(file);
        }
    }

    async uploadFile(file) {
        // Validate file
        if (!file.name.endsWith('.md')) {
            this.showToast('Only .md files are allowed', 'error');
            return;
        }
        
        if (file.size > 100 * 1024) { // 100KB
            this.showToast('File too large (max 100KB)', 'error');
            return;
        }

        const formData = new FormData();
        formData.append('file', file);

        try {
            const response = await fetch('/api/upload', {
                method: 'POST',
                body: formData
            });

            if (response.ok) {
                this.showToast('File uploaded successfully', 'success');
                this.refreshData();
            } else if (response.status === 409) {
                this.showToast('Architect is busy', 'error');
            } else {
                this.showToast('Upload failed', 'error');
            }
        } catch (error) {
            console.error('Upload error:', error);
            this.showToast('Upload failed', 'error');
        }
    }

    // Queue management
    async toggleQueue(queueName) {
        const content = document.getElementById(`${queueName}-content`);
        const chevron = document.getElementById(`${queueName}-chevron`);
        
        if (content.classList.contains('hidden')) {
            content.classList.remove('hidden');
            chevron.classList.add('rotate-180');
            this.startQueuePolling(queueName);
        } else {
            content.classList.add('hidden');
            chevron.classList.remove('rotate-180');
            this.stopQueuePolling(queueName);
        }
    }

    startQueuePolling(queueName) {
        this.stopQueuePolling(queueName); // Clear any existing interval
        this.updateQueue(queueName); // Initial update
        this.queuePollingIntervals[queueName] = setInterval(() => {
            this.updateQueue(queueName);
        }, this.pollingInterval);
    }

    stopQueuePolling(queueName) {
        if (this.queuePollingIntervals[queueName]) {
            clearInterval(this.queuePollingIntervals[queueName]);
            delete this.queuePollingIntervals[queueName];
        }
    }

    async updateQueue(queueName) {
        try {
            const response = await fetch('/api/queues');
            if (!response.ok) throw new Error('Failed to fetch queues');
            
            const queues = await response.json();
            this.updateQueueDisplay(queueName, queues[queueName]);
            
            // Update count badges
            document.getElementById(`architect-count`).textContent = queues.architect?.length || 0;
            document.getElementById(`coder-count`).textContent = queues.coder?.length || 0;
            document.getElementById(`shared-count`).textContent = queues.shared?.length || 0;
            
        } catch (error) {
            console.error('Error updating queue:', error);
        }
    }

    updateQueueDisplay(queueName, queueData) {
        const tbody = document.getElementById(`${queueName}-queue-body`);
        tbody.innerHTML = '';
        
        if (!queueData || !queueData.heads) return;
        
        queueData.heads.forEach(msg => {
            const row = document.createElement('tr');
            row.innerHTML = `
                <td class="px-3 py-2 text-sm text-gray-900">${msg.id}</td>
                <td class="px-3 py-2 text-sm text-gray-900">${msg.type}</td>
                <td class="px-3 py-2 text-sm text-gray-900">${msg.from}</td>
                <td class="px-3 py-2 text-sm text-gray-900">${msg.to}</td>
                <td class="px-3 py-2 text-sm text-gray-500">${new Date(msg.ts).toLocaleString()}</td>
            `;
            tbody.appendChild(row);
        });
    }

    // Control actions
    async cancelRun() {
        const button = document.getElementById('cancel-run');
        const text = document.getElementById('cancel-text');
        const spinner = document.getElementById('cancel-spinner');
        
        button.disabled = true;
        text.textContent = 'Stopping...';
        spinner.classList.remove('hidden');
        
        try {
            const response = await fetch('/api/shutdown', { method: 'POST' });
            if (response.ok) {
                this.showToast('Shutdown initiated', 'success');
                // Keep polling to show when agents actually stop
            } else {
                this.showToast('Shutdown failed', 'error');
            }
        } catch (error) {
            console.error('Shutdown error:', error);
            this.showToast('Shutdown failed', 'error');
        } finally {
            setTimeout(() => {
                button.disabled = false;
                text.textContent = 'Cancel Run';
                spinner.classList.add('hidden');
            }, 2000);
        }
    }

    refreshData() {
        this.pollAgents();
        this.pollLogs();
    }

    showEscalations() {
        // TODO: Implement escalation modal
        this.showToast('Escalation handling not yet implemented', 'info');
    }

    closeModal() {
        document.getElementById('escalation-modal').classList.add('hidden');
    }

    // Event handlers
    onLogDomainChange() {
        this.pollLogs();
    }

    onAutoscrollChange(e) {
        this.autoscroll = e.target.checked;
    }

    clearLogs() {
        document.getElementById('logs-container').innerHTML = '';
    }

    // Connection management
    setConnectionStatus(connected) {
        const indicator = document.getElementById('status-indicator');
        const banner = document.getElementById('offline-banner');
        
        if (connected && !this.isConnected) {
            // Reconnected
            this.isConnected = true;
            this.failureCount = 0;
            indicator.innerHTML = `
                <div class="w-2 h-2 bg-green-500 rounded-full mr-2"></div>
                <span class="text-sm text-gray-600">Connected</span>
            `;
            banner.classList.add('hidden');
        } else if (!connected && this.isConnected) {
            // Disconnected
            this.isConnected = false;
            indicator.innerHTML = `
                <div class="w-2 h-2 bg-red-500 rounded-full mr-2"></div>
                <span class="text-sm text-gray-600">Disconnected</span>
            `;
            banner.classList.remove('hidden');
        }
    }

    handleConnectionError() {
        this.failureCount++;
        if (this.failureCount >= this.maxFailures) {
            this.setConnectionStatus(false);
        }
    }

    updateLastUpdated() {
        const now = new Date();
        const timeString = now.toLocaleTimeString();
        document.getElementById('last-updated').textContent = timeString;
    }

    // Toast notifications
    showToast(message, type = 'info') {
        const container = document.getElementById('toast-container');
        const toast = document.createElement('div');
        
        const bgColors = {
            success: 'bg-green-500',
            error: 'bg-red-500',
            warning: 'bg-yellow-500',
            info: 'bg-blue-500'
        };
        
        toast.className = `${bgColors[type]} text-white px-4 py-2 rounded-md shadow-lg transform transition-all duration-300 translate-x-full`;
        toast.textContent = message;
        
        container.appendChild(toast);
        
        // Animate in
        setTimeout(() => {
            toast.classList.remove('translate-x-full');
        }, 100);
        
        // Remove after 3 seconds
        setTimeout(() => {
            toast.classList.add('translate-x-full');
            setTimeout(() => container.removeChild(toast), 300);
        }, 3000);
    }
}

// Global functions for onclick handlers
function toggleQueue(queueName) {
    window.maestroUI.toggleQueue(queueName);
}

// Initialize when DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
    window.maestroUI = new MaestroUI();
});