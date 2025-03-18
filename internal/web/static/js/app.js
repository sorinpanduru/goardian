// ProcessCard component displays information about a single process group
const ProcessCard = ({ group, onStart, onStop, onRestart }) => {
    const totalInstances = group.processes.length;
    const runningInstances = group.processes.filter(p => p.state === 0).length;
    const failedInstances = group.processes.filter(p => p.state === 2).length;
    
    // Determine overall status color based on instance states
    let statusColor = 'green';
    if (failedInstances > 0) {
        statusColor = 'red';
    } else if (runningInstances < totalInstances) {
        statusColor = 'yellow';
    }

    return (
        <div className="bg-white rounded-lg shadow-md p-6 mb-4">
            <div className="flex justify-between items-center mb-4">
                <h2 className="text-xl font-semibold">{group.name}</h2>
                <div className="flex space-x-2">
                    <button
                        onClick={() => onStart(group.name)}
                        className="bg-green-500 text-white px-4 py-2 rounded hover:bg-green-600"
                    >
                        Start
                    </button>
                    <button
                        onClick={() => onStop(group.name)}
                        className="bg-red-500 text-white px-4 py-2 rounded hover:bg-red-600"
                    >
                        Stop
                    </button>
                    <button
                        onClick={() => onRestart(group.name)}
                        className="bg-blue-500 text-white px-4 py-2 rounded hover:bg-blue-600"
                    >
                        Restart
                    </button>
                </div>
            </div>
            <div className="grid grid-cols-2 gap-4 mb-4">
                <div>
                    <p className="text-gray-600">Command</p>
                    <p className="font-mono">{group.command} {group.args.join(' ')}</p>
                </div>
                <div>
                    <p className="text-gray-600">Instances</p>
                    <p>
                        <span className={`inline-block w-3 h-3 rounded-full bg-${statusColor}-500 mr-2`}></span>
                        {runningInstances}/{totalInstances} running
                        {failedInstances > 0 && <span className="ml-2 text-red-600">{failedInstances} failed</span>}
                    </p>
                </div>
            </div>
            <div className="border-t pt-4">
                <h3 className="text-lg font-semibold mb-2">Instances</h3>
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                    {group.processes.map((proc, idx) => {
                        const statusClass = getStatusColor(proc.state);
                        const statusText = getStatusText(proc.state);
                        const isRunning = proc.state === 0; // StateRunning = 0
                        
                        return (
                            <div
                                key={idx}
                                className={`p-4 rounded shadow-sm ${statusClass}`}
                            >
                                <p className="font-semibold">Instance {proc.instanceId}</p>
                                <p className={`text-sm mb-2`}>
                                    {statusText}
                                </p>
                                
                                {isRunning && (
                                    <div className="mt-2 space-y-1 text-sm">
                                        <div className="flex justify-between">
                                            <span className="text-gray-600">Uptime:</span>
                                            <span className="font-medium">{proc.uptimeString || '0 sec'}</span>
                                        </div>
                                        <div className="flex justify-between">
                                            <span className="text-gray-600">Memory:</span>
                                            <span className="font-medium">{proc.memoryString || '0 B'}</span>
                                        </div>
                                        {proc.memoryMB > 0 && (
                                            <div className="mt-1">
                                                <div className="w-full bg-gray-200 rounded-full h-2">
                                                    <div 
                                                        className={`h-2 rounded-full ${getMemoryColor(proc.memoryMB)}`}
                                                        style={{ width: `${Math.min(proc.memoryMB / 10 * 100, 100)}%` }}
                                                    ></div>
                                                </div>
                                            </div>
                                        )}
                                        {proc.backoffState && proc.backoffState.consecutiveFailures > 0 && (
                                            <div className="flex justify-between">
                                                <span className="text-gray-600">Failures:</span>
                                                <span className="font-medium text-orange-500">{proc.backoffState.consecutiveFailures}</span>
                                            </div>
                                        )}
                                        {proc.restartStats && (
                                            <div className="mt-2 space-y-1">
                                                <div className="flex justify-between">
                                                    <span className="text-gray-600">Total Restarts:</span>
                                                    <span className="font-medium">{proc.restartStats.totalRestarts}</span>
                                                </div>
                                                <div className="flex justify-between">
                                                    <span className="text-gray-600">Failure Restarts:</span>
                                                    <span className="font-medium text-red-500">{proc.restartStats.failureRestarts}</span>
                                                </div>
                                            </div>
                                        )}
                                    </div>
                                )}
                                
                                {proc.state === 2 && proc.restartStats && (
                                    <div className="mt-2 space-y-1 text-sm">
                                        <div className="flex justify-between">
                                            <span className="text-gray-600">Max Restarts:</span>
                                            <span className="font-medium text-red-500">Reached</span>
                                        </div>
                                        <div className="flex justify-between">
                                            <span className="text-gray-600">Total Restarts:</span>
                                            <span className="font-medium">{proc.restartStats.totalRestarts}</span>
                                        </div>
                                        <div className="flex justify-between">
                                            <span className="text-gray-600">Failure Restarts:</span>
                                            <span className="font-medium text-red-500">{proc.restartStats.failureRestarts}</span>
                                        </div>
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>
            </div>
        </div>
    );
};

// Main App component
const App = () => {
    const [processes, setProcesses] = React.useState([]);
    const [error, setError] = React.useState(null);
    const [wsStatus, setWsStatus] = React.useState('connecting');
    const wsRef = React.useRef(null);
    const reconnectTimeoutRef = React.useRef(null);
    const reconnectAttemptsRef = React.useRef(0);

    // Function to establish WebSocket connection
    const connectWebSocket = React.useCallback(() => {
        if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
            return; // Already connected
        }

        // Clear any existing reconnection timeouts
        if (reconnectTimeoutRef.current) {
            clearTimeout(reconnectTimeoutRef.current);
            reconnectTimeoutRef.current = null;
        }

        setWsStatus('connecting');
        const ws = new WebSocket(`ws://${window.location.host}/ws`);
        wsRef.current = ws;

        ws.onopen = () => {
            setWsStatus('connected');
            setError(null);
            reconnectAttemptsRef.current = 0; // Reset reconnect attempts on successful connection
        };

        ws.onmessage = (event) => {
            const data = JSON.parse(event.data);
            if (data.type === 'process_state') {
                // Debug log the state value
                console.log(`Process state update - Group: ${data.group}, Instance: ${data.process}, State: ${data.data.state}`);
                
                setProcesses(prev => prev.map(group => {
                    if (group.name === data.group) {
                        return {
                            ...group,
                            processes: group.processes.map(proc => {
                                if (proc.instanceId === data.process) {
                                    return { 
                                        ...proc, 
                                        running: data.data.running,
                                        state: data.data.state,
                                        uptime: data.data.uptime,
                                        uptimeString: data.data.uptimeString,
                                        memoryBytes: data.data.memoryBytes,
                                        memoryMB: data.data.memoryMB,
                                        memoryString: data.data.memoryString,
                                        startTime: data.data.startTime,
                                        backoffState: data.data.backoffState,
                                        restartStats: data.data.restartStats
                                    };
                                }
                                return proc;
                            })
                        };
                    }
                    return group;
                }));
            }
        };

        ws.onerror = () => {
            setWsStatus('error');
            setError('WebSocket connection error. Attempting to reconnect...');
        };

        ws.onclose = () => {
            setWsStatus('disconnected');
            
            // Implement exponential backoff for reconnection
            const maxReconnectDelay = 30000; // Maximum 30 seconds
            const baseDelay = 1000; // Start with 1 second
            reconnectAttemptsRef.current += 1;
            
            // Calculate delay with exponential backoff (2^attempts * baseDelay)
            const delay = Math.min(
                maxReconnectDelay, 
                Math.pow(2, Math.min(reconnectAttemptsRef.current, 5)) * baseDelay
            );
            
            // Add jitter to prevent all clients reconnecting simultaneously
            const jitteredDelay = delay * (0.8 + Math.random() * 0.4);
            
            setError(`WebSocket disconnected. Reconnecting in ${Math.round(jitteredDelay/1000)}s...`);
            
            // Schedule reconnection
            reconnectTimeoutRef.current = setTimeout(() => {
                connectWebSocket();
            }, jitteredDelay);
        };
    }, []);

    // Connect to WebSocket and fetch initial data
    React.useEffect(() => {
        // Fetch initial process state
        fetch('/api/processes')
            .then(res => res.json())
            .then(data => setProcesses(data))
            .catch(err => setError('Failed to load processes'));

        // Initial WebSocket connection
        connectWebSocket();

        // Ping to keep connection alive (every 15 seconds)
        const pingInterval = setInterval(() => {
            if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
                wsRef.current.send(JSON.stringify({ type: 'ping' }));
            }
        }, 15000);

        // Cleanup function
        return () => {
            clearInterval(pingInterval);
            if (reconnectTimeoutRef.current) {
                clearTimeout(reconnectTimeoutRef.current);
            }
            if (wsRef.current) {
                wsRef.current.close();
            }
        };
    }, [connectWebSocket]);

    // Process control functions
    const handleStart = async (groupName) => {
        try {
            await fetch(`/api/processes/${groupName}/start`, { method: 'POST' });
        } catch (err) {
            setError(`Failed to start ${groupName}`);
        }
    };

    const handleStop = async (groupName) => {
        try {
            await fetch(`/api/processes/${groupName}/stop`, { method: 'POST' });
        } catch (err) {
            setError(`Failed to stop ${groupName}`);
        }
    };

    const handleRestart = async (groupName) => {
        try {
            await fetch(`/api/processes/${groupName}/restart`, { method: 'POST' });
        } catch (err) {
            setError(`Failed to restart ${groupName}`);
        }
    };

    return (
        <div className="container mx-auto px-4 py-8">
            <header className="mb-8">
                <h1 className="text-3xl font-bold text-gray-900">Goardian Dashboard</h1>
                <p className="text-gray-600">Process Management Interface</p>
                <div className={`mt-2 inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                    wsStatus === 'connected' ? 'bg-green-100 text-green-800' : 
                    wsStatus === 'connecting' ? 'bg-yellow-100 text-yellow-800' : 
                    'bg-red-100 text-red-800'
                }`}>
                    {wsStatus === 'connected' ? 'Connected' : 
                     wsStatus === 'connecting' ? 'Connecting...' : 
                     'Disconnected'}
                </div>
            </header>

            {error && (
                <div className="bg-red-100 border border-red-400 text-red-700 px-4 py-3 rounded mb-4">
                    {error}
                </div>
            )}

            <div className="space-y-6">
                {processes.map(group => (
                    <ProcessCard
                        key={group.name}
                        group={group}
                        onStart={handleStart}
                        onStop={handleStop}
                        onRestart={handleRestart}
                    />
                ))}
            </div>
        </div>
    );
};

// Helper function to get color for memory usage
const getMemoryColor = (memoryMB) => {
    if (memoryMB < 50) return 'bg-green-500';
    if (memoryMB < 200) return 'bg-yellow-500';
    return 'bg-red-500';
};

// Helper function to get status color
const getStatusColor = (status) => {
    switch (status) {
        case 0:  // StateRunning
            return 'bg-green-100 text-green-800';
        case 1:  // StateStopped
            return 'bg-gray-100 text-gray-800';
        case 2:  // StateFailed
            return 'bg-red-100 text-red-800';
        default:
            return 'bg-gray-100 text-gray-800';
    }
};

// Helper function to get status text
const getStatusText = (status) => {
    switch (status) {
        case 0:  // StateRunning
            return 'Running';
        case 1:  // StateStopped
            return 'Stopped';
        case 2:  // StateFailed
            return 'Failed';
        default:
            return 'Unknown';
    }
};

// Render the app
ReactDOM.render(<App />, document.getElementById('root')); 