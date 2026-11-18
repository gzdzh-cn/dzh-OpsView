export const marketCategories = [
  { id: 'all', label: '全部' },
  { id: 'installed', label: '已安装' },
  { id: 'runtime', label: '运行环境' },
  { id: 'free', label: '免费应用' },
  { id: 'security', label: '安全应用' },
  { id: 'docker', label: 'Docker应用' },
  { id: 'professional', label: '专业版应用' },
  { id: 'enterprise', label: '企业版应用' },
] as const
export function pluginCategory(p: any): string {
  if (p.type === 'application' || p.permissions?.includes('docker.compose')) return 'docker'
  return ['runtime', 'security', 'free'].includes(p.category) ? p.category : 'free'
}
export function pluginEdition(p: any): string {
  return ['professional', 'enterprise'].includes(p.edition) ? p.edition : 'free'
}
export function matchesMarketCategory(
  row: { category: string; edition: string; installed: boolean },
  category: string,
) {
  if (category === 'all') return true
  if (category === 'installed') return row.installed
  if (['free', 'professional', 'enterprise'].includes(category)) return row.edition === category
  return row.category === category
}
