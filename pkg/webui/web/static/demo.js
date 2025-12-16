// Demo Tab Controller
// Manages demo service start/stop/restart/rebuild and status display

class DemoController {
    constructor() {
        this.isRunning = false;
        this.isAvailable = false;
        this.pollInterval = null;
        this.initializeEventListeners();
        this.startStatusPolling();
    }

    initializeEventListeners() {
        // Action buttons
        document.getElementById('demo-start-btn')?.addEventListener('click', () => this.startDemo());
        document.getElementById('demo-stop-btn')?.addEventListener('click', () => this.stopDemo());
        document.getElementById('demo-restart-btn')?.addEventListener('click', () => this.restartDemo());
        document.getElementById('demo-rebuild-btn')?.addEventListener('click', () => this.rebuildDemo());

        // Open browser button
        document.getElementById('demo-open-browser')?.addEventListener('click', () => this.openInBrowser());

        // Refresh logs button
        document.getElementById('demo-refresh-logs')?.addEventListener('click', () => this.fetchLogs());
    }

    startStatusPolling() {
        // Initial fetch
        this.fetchStatus();

        // Poll every 3 seconds
        this.pollInterval = setInterval(() => this.fetchStatus(), 3000);
    }

    stopStatusPolling() {
        if (this.pollInterval) {
            clearInterval(this.pollInterval);
            this.pollInterval = null;
        }
    }

    async fetchStatus() {
        try {
            const response = await fetch('/api/demo/status');

            if (response.status === 503) {
                // Demo service not available
                this.updateUI({ available: false, running: false });
                return;
            }

            if (!response.ok) {
                console.error('Failed to fetch demo status:', response.statusText);
                return;
            }

            const status = await response.json();
            this.updateUI({ available: true, ...status });
        } catch (error) {
            console.error('Error fetching demo status:', error);
            this.updateUI({ available: false, running: false });
        }
    }

    updateUI(status) {
        this.isAvailable = status.available !== false;
        this.isRunning = status.running || false;

        const badge = document.getElementById('demo-status-badge');
        const details = document.getElementById('demo-details');
        const notAvailable = document.getElementById('demo-not-available');
        const startBtn = document.getElementById('demo-start-btn');
        const stopBtn = document.getElementById('demo-stop-btn');
        const restartBtn = document.getElementById('demo-restart-btn');
        const rebuildBtn = document.getElementById('demo-rebuild-btn');

        if (!this.isAvailable) {
            // Demo service not available - show the actual reason from API
            badge.textContent = 'Unavailable';
            badge.className = 'px-3 py-1 rounded-full text-sm font-medium bg-gray-100 text-gray-600';
            details.classList.add('hidden');
            notAvailable.classList.remove('hidden');

            // Update the reason text dynamically
            const reasonText = document.getElementById('demo-not-available-reason');
            if (reasonText && status.reason) {
                reasonText.textContent = status.reason;
            }

            startBtn.disabled = true;
            stopBtn.disabled = true;
            restartBtn.disabled = true;
            rebuildBtn.disabled = true;
            return;
        }

        notAvailable.classList.add('hidden');

        if (this.isRunning) {
            badge.textContent = 'Running';
            badge.className = 'px-3 py-1 rounded-full text-sm font-medium bg-green-100 text-green-800';
            details.classList.remove('hidden');

            // Update details
            const url = status.url || `http://localhost:${status.port || 8081}`;
            document.getElementById('demo-url').href = url;
            document.getElementById('demo-url').textContent = url;
            document.getElementById('demo-port').textContent = status.port || '-';
            document.getElementById('demo-health').textContent = status.healthy ? 'Healthy' : 'Unhealthy';
            document.getElementById('demo-health').className = status.healthy
                ? 'text-green-600 font-medium'
                : 'text-yellow-600 font-medium';

            // Calculate uptime
            if (status.started_at) {
                const uptime = this.formatUptime(new Date(status.started_at));
                document.getElementById('demo-uptime').textContent = uptime;
            } else {
                document.getElementById('demo-uptime').textContent = '-';
            }

            // Button states
            startBtn.disabled = true;
            stopBtn.disabled = false;
            restartBtn.disabled = false;
            rebuildBtn.disabled = false;
        } else {
            badge.textContent = 'Stopped';
            badge.className = 'px-3 py-1 rounded-full text-sm font-medium bg-gray-100 text-gray-600';
            details.classList.add('hidden');

            // Button states
            startBtn.disabled = false;
            stopBtn.disabled = true;
            restartBtn.disabled = true;
            rebuildBtn.disabled = false; // Can rebuild even when stopped
        }
    }

    formatUptime(startedAt) {
        const now = new Date();
        const diff = Math.floor((now - startedAt) / 1000);

        if (diff < 60) {
            return `${diff}s`;
        } else if (diff < 3600) {
            const mins = Math.floor(diff / 60);
            const secs = diff % 60;
            return `${mins}m ${secs}s`;
        } else {
            const hours = Math.floor(diff / 3600);
            const mins = Math.floor((diff % 3600) / 60);
            return `${hours}h ${mins}m`;
        }
    }

    async startDemo() {
        const btn = document.getElementById('demo-start-btn');
        btn.disabled = true;
        btn.innerHTML = '<span class="animate-spin inline-block w-4 h-4 mr-2 border-2 border-white border-t-transparent rounded-full"></span>Starting...';

        try {
            const response = await fetch('/api/demo/start', { method: 'POST' });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            // Fetch updated status and logs
            await this.fetchStatus();
            await this.fetchLogs();
        } catch (error) {
            alert(`Failed to start demo: ${error.message}`);
        } finally {
            btn.innerHTML = `<svg class="w-4 h-4 mr-2 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z" />
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>Start`;
            this.fetchStatus();
        }
    }

    async stopDemo() {
        const btn = document.getElementById('demo-stop-btn');
        btn.disabled = true;
        btn.innerHTML = '<span class="animate-spin inline-block w-4 h-4 mr-2 border-2 border-gray-600 border-t-transparent rounded-full"></span>Stopping...';

        try {
            const response = await fetch('/api/demo/stop', { method: 'POST' });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            await this.fetchStatus();
        } catch (error) {
            alert(`Failed to stop demo: ${error.message}`);
        } finally {
            btn.innerHTML = `<svg class="w-4 h-4 mr-2 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 10a1 1 0 011-1h4a1 1 0 011 1v4a1 1 0 01-1 1h-4a1 1 0 01-1-1v-4z" />
            </svg>Stop`;
            this.fetchStatus();
        }
    }

    async restartDemo() {
        const btn = document.getElementById('demo-restart-btn');
        btn.disabled = true;
        btn.innerHTML = '<span class="animate-spin inline-block w-4 h-4 mr-2 border-2 border-gray-600 border-t-transparent rounded-full"></span>Restarting...';

        try {
            const response = await fetch('/api/demo/restart', { method: 'POST' });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            await this.fetchStatus();
            await this.fetchLogs();
        } catch (error) {
            alert(`Failed to restart demo: ${error.message}`);
        } finally {
            btn.innerHTML = `<svg class="w-4 h-4 mr-2 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>Restart`;
            this.fetchStatus();
        }
    }

    async rebuildDemo() {
        if (!confirm('Rebuild will stop the demo, rebuild all containers from scratch, and restart. This may take a few minutes. Continue?')) {
            return;
        }

        const btn = document.getElementById('demo-rebuild-btn');
        btn.disabled = true;
        btn.innerHTML = '<span class="animate-spin inline-block w-4 h-4 mr-2 border-2 border-gray-600 border-t-transparent rounded-full"></span>Rebuilding...';

        try {
            const response = await fetch('/api/demo/rebuild', { method: 'POST' });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            await this.fetchStatus();
            await this.fetchLogs();
        } catch (error) {
            alert(`Failed to rebuild demo: ${error.message}`);
        } finally {
            btn.innerHTML = `<svg class="w-4 h-4 mr-2 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19.428 15.428a2 2 0 00-1.022-.547l-2.387-.477a6 6 0 00-3.86.517l-.318.158a6 6 0 01-3.86.517L6.05 15.21a2 2 0 00-1.806.547M8 4h8l-1 1v5.172a2 2 0 00.586 1.414l5 5c1.26 1.26.367 3.414-1.415 3.414H4.828c-1.782 0-2.674-2.154-1.414-3.414l5-5A2 2 0 009 10.172V5L8 4z" />
            </svg>Rebuild`;
            this.fetchStatus();
        }
    }

    async fetchLogs() {
        try {
            const response = await fetch('/api/demo/logs');

            if (!response.ok) {
                if (response.status === 503) {
                    document.getElementById('demo-logs-content').textContent = 'Demo service not available.';
                    return;
                }
                throw new Error(response.statusText);
            }

            const data = await response.json();
            const logsContent = document.getElementById('demo-logs-content');

            if (data.logs && data.logs.length > 0) {
                logsContent.textContent = data.logs;
                // Auto-scroll to bottom
                const logsContainer = document.getElementById('demo-logs');
                logsContainer.scrollTop = logsContainer.scrollHeight;
            } else {
                logsContent.textContent = 'No logs available. Start the demo to see logs.';
            }
        } catch (error) {
            console.error('Error fetching demo logs:', error);
            document.getElementById('demo-logs-content').textContent = `Error fetching logs: ${error.message}`;
        }
    }

    openInBrowser() {
        const url = document.getElementById('demo-url')?.href;
        if (url && url !== '#') {
            window.open(url, '_blank');
        }
    }
}

// Initialize demo controller when page loads
let demoController;
document.addEventListener('DOMContentLoaded', () => {
    // Only initialize if demo tab exists
    if (document.getElementById('pm-demo-content')) {
        demoController = new DemoController();
    }
});
