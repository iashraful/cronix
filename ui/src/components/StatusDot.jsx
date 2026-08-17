export default function StatusDot({ status }) {
  const cls = status === 'ok' || status === 'failed' || status === 'error' ? status : 'never'
  return <span aria-hidden="true" className={`dot ${cls}`} />
}
