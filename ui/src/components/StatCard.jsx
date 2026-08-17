export default function StatCard({ label, value, tone }) {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className={`stat-value ${tone ? `tone-${tone}` : ''}`}>{value}</span>
    </div>
  )
}
