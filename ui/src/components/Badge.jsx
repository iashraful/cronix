import StatusDot from './StatusDot'

export default function Badge({ lastRun }) {
  if (!lastRun) {
    return <span className="pill never"><StatusDot status={null} />never</span>
  }
  const label = lastRun.status === 'failed'
    ? `failed ${lastRun.exit_code}`
    : lastRun.status
  return (
    <span className={`pill ${lastRun.status}`}>
      <StatusDot status={lastRun.status} />
      {label}
    </span>
  )
}
