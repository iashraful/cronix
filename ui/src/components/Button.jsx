export default function Button({ variant = 'ghost', size = 'md', href, className = '', type = 'button', children, ...rest }) {
  const cls = `btn ${variant} ${size === 'sm' ? 'sm' : ''} ${className}`.trim()
  if (href) {
    return (
      <a className={cls} href={href} {...rest}>
        {children}
      </a>
    )
  }
  return (
    <button type={type} className={cls} {...rest}>
      {children}
    </button>
  )
}
