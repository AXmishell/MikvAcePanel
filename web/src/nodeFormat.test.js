import { describe, expect, it } from 'vitest'
import { agentNeedsUpgrade, coreLabel, diskPct, memPct, rankingLabel, runtimeLabel, runtimeMode, versionLabel } from './nodeFormat.js'

describe('node presentation helpers', () => {
  it('labels cores and runtime modes', () => {
    expect(coreLabel({ cores: ['hysteria', 'xray'] })).toBe('Hy2 + Xray')
    expect(coreLabel({ type: 'xray' })).toBe('Xray')
    expect(runtimeMode({ runtimeMode: 'docker' })).toBe('docker')
    expect(runtimeMode({})).toBe('process')
    expect(runtimeLabel('docker')).toBe('Docker 模式')
    expect(runtimeLabel('process')).toBe('进程模式')
  })

  it('builds version labels', () => {
    const label = versionLabel({ cores: ['hysteria', 'xray'], agentVersion: '1.2.3', hy2Version: '2.6.0', xrayVersion: '25.1' })
    expect(label).toContain('Agent 1.2.3')
    expect(label).toContain('Hysteria 2.6.0')
    expect(label).toContain('Xray 25.1')
    expect(versionLabel({})).toBe('Hysteria unknown')
  })

  it('detects agent version drift', () => {
    expect(agentNeedsUpgrade({ agentVersion: '1.0.0' }, '1.0.1')).toBe(true)
    expect(agentNeedsUpgrade({ agentVersion: '1.0.0' }, '1.0.0')).toBe(false)
    expect(agentNeedsUpgrade({}, '1.0.0')).toBe(false)
  })

  it('computes resource percentages safely', () => {
    expect(memPct({ metrics: { memUsed: 50, memTotal: 200 } })).toBe(25)
    expect(memPct({})).toBe(0)
    expect(diskPct({ metrics: { diskUsed: 90, diskTotal: 100 } })).toBe(90)
    expect(diskPct({ metrics: { diskUsed: 200, diskTotal: 100 } })).toBe(100)
  })

  it('formats ranking weights', () => {
    expect(rankingLabel(1)).toBe('1')
    expect(rankingLabel(1.234)).toBe('1.23')
    expect(rankingLabel('bad')).toBe('1')
  })
})
