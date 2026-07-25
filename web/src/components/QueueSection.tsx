import React from 'react';
import type { QueueSnapshot, JobPriority, QueuedJob } from '../types';

interface QueueSectionProps {
  snapshot: QueueSnapshot | null;
  onSetPriority: (jobId: string, priority: JobPriority) => void;
  onReorder: (priority: JobPriority, jobIds: string[]) => void;
  onPause: (jobId: string) => void;
  onResume: (jobId: string) => void;
  onCancel: (jobId: string) => void;
}

export const QueueSection: React.FC<QueueSectionProps> = ({
  snapshot,
  onSetPriority,
  onReorder,
  onPause,
  onResume,
  onCancel,
}) => {
  if (!snapshot) {
    return null;
  }

  const lanes: { priority: JobPriority; label: string; icon: string }[] = [
    { priority: 'high', label: 'High Priority', icon: '🔥' },
    { priority: 'normal', label: 'Normal Priority', icon: '⚡' },
    { priority: 'low', label: 'Low Priority', icon: '🐢' },
  ];

  const handleMove = (priority: JobPriority, items: QueuedJob[], index: number, direction: 'up' | 'down') => {
    const newItems = [...items];
    const targetIndex = direction === 'up' ? index - 1 : index + 1;
    if (targetIndex < 0 || targetIndex >= newItems.length) return;

    const temp = newItems[index];
    newItems[index] = newItems[targetIndex];
    newItems[targetIndex] = temp;

    const reorderedIDs = newItems.map((item) => item.jobId);
    onReorder(priority, reorderedIDs);
  };

  return (
    <div className="queue-section">
      <div className="queue-summary-bar">
        <div className="queue-stat">
          <span className="stat-label">Active Slots:</span>
          <span className="stat-value highlight">{snapshot.runningDownloads} / {snapshot.maxConcurrentDownloads}</span>
        </div>
        <div className="queue-stat">
          <span className="stat-label">Waiting in Queue:</span>
          <span className="stat-value">{snapshot.queuedDownloads}</span>
        </div>
        <div className="queue-stat">
          <span className="stat-label">Paused:</span>
          <span className="stat-value">{snapshot.pausedDownloads}</span>
        </div>
      </div>

      <div className="queue-lanes">
        {lanes.map(({ priority, label, icon }) => {
          const laneItems = snapshot.items.filter(
            (item) => (item.job?.priority || 'normal') === priority
          );

          return (
            <div key={priority} className={`queue-lane queue-lane-${priority}`}>
              <div className="lane-header">
                <h4>
                  <span className="lane-icon">{icon}</span> {label} ({laneItems.length})
                </h4>
              </div>

              {laneItems.length === 0 ? (
                <div className="lane-empty">No items queued in {label.toLowerCase()}</div>
              ) : (
                <div className="lane-items">
                  {laneItems.map((item, idx) => {
                    const job = item.job;
                    const jobName = job?.name || item.jobId;

                    return (
                      <div key={item.jobId} className="queue-item">
                        <div className="queue-item-main">
                          <span className="queue-pos">#{item.position}</span>
                          <div className="queue-item-details">
                            <span className="queue-item-title" title={jobName}>{jobName}</span>
                            <div className="queue-badges">
                              <span className={`badge status-badge status-${job?.status}`}>
                                {job?.status?.toUpperCase()}
                              </span>
                              <span className="badge reason-badge">
                                {item.waitingReason === 'paused_by_user' ? '⏸️ Paused by user' : '⏳ Waiting for download slot'}
                              </span>
                              <span className="badge action-badge">
                                {item.action === 'start' ? 'Fresh start' : 'Resume'}
                              </span>
                            </div>
                          </div>
                        </div>

                        <div className="queue-item-actions">
                          <select
                            className="priority-select-sm"
                            value={priority}
                            onChange={(e) => onSetPriority(item.jobId, e.target.value as JobPriority)}
                          >
                            <option value="high">High</option>
                            <option value="normal">Normal</option>
                            <option value="low">Low</option>
                          </select>

                          <div className="reorder-btn-group">
                            <button
                              type="button"
                              className="btn-icon"
                              disabled={idx === 0}
                              onClick={() => handleMove(priority, laneItems, idx, 'up')}
                              title="Move Up in Lane"
                            >
                              ▲
                            </button>
                            <button
                              type="button"
                              className="btn-icon"
                              disabled={idx === laneItems.length - 1}
                              onClick={() => handleMove(priority, laneItems, idx, 'down')}
                              title="Move Down in Lane"
                            >
                              ▼
                            </button>
                          </div>

                          {job?.status === 'paused' ? (
                            <button
                              type="button"
                              className="btn btn-sm btn-secondary"
                              onClick={() => onResume(item.jobId)}
                            >
                              Resume
                            </button>
                          ) : (
                            <button
                              type="button"
                              className="btn btn-sm btn-secondary"
                              onClick={() => onPause(item.jobId)}
                            >
                              Pause
                            </button>
                          )}

                          <button
                            type="button"
                            className="btn btn-sm btn-danger"
                            onClick={() => onCancel(item.jobId)}
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
};
