// ProcessCard component displays information about a single process group
const ProcessCard = ({ group, onStart, onStop, onRestart }) => {
    const totalInstances = group.processes.length;
    const runningInstances = group.processes.filter(p => p.running).length;
    const statusColor = runningInstances === totalInstances ? 'green' : runningInstances === 0 ? 'red' : 'yellow';

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
                    </p>
                </div>
            </div>
            <div className="border-t pt-4">
                <h3 className="text-lg font-semibold mb-2">Instances</h3>
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                    {group.processes.map((proc, idx) => (
                        <div
                            key={idx}
                            className={`p-4 rounded shadow-sm ${proc.running ? 'bg-green-100' : 'bg-red-100'}`}
                        >
                            <p className="font-semibold">Instance {proc.instanceId}</p>
                            <p className={`text-sm ${proc.running ? 'text-green-600' : 'text-red-600'} mb-2`}>
                                {proc.running ? 'Running' : 'Stopped'}
                            </p>
                            
                            {proc.running && (
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
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
};

// Main App component
const App = () => {
    const [processes, setProcesses] = React.useState([]);
    const [error, setError] = React.useState(null);
    const wsRef = React.useRef(null);

    // Connect to WebSocket and fetch initial data
    React.useEffect(() => {
        // Fetch initial process state
        fetch('/api/processes')
            .then(res => res.json())
            .then(data => setProcesses(data))
            .catch(err => setError('Failed to load processes'));

        // Connect to WebSocket for real-time updates
        const ws = new WebSocket(`ws://${window.location.host}/ws`);
        wsRef.current = ws;

        ws.onmessage = (event) => {
            const data = JSON.parse(event.data);
            if (data.type === 'process_state') {
                setProcesses(prev => prev.map(group => {
                    if (group.name === data.group) {
                        return {
                            ...group,
                            processes: group.processes.map(proc => {
                                if (proc.instanceId === data.process) {
                                    return { 
                                        ...proc, 
                                        running: data.data.running,
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
            setError('WebSocket connection failed');
        };

        return () => {
            if (ws) ws.close();
        };
    }, []);

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

// Render the app
ReactDOM.render(<App />, document.getElementById('root')); 