import { describe, expect, it, vi, afterEach } from 'vitest'
import { bytesToGb, fmtBytes, fmtDate, fmtRelative, gbToBytes, pct, ratioClass, shortToken } from './format.js'

afterEach(() => {
  vi.useRealTimers()
})

describe('format helpers', () => {
  it('formats byte sizes with binary units', () => {
    expect(fmtBytes(0)).toBe('0 B')
    expect(fmtBytes(1024)).toBe('1.0 KB')
    expect(fmtBytes(1536, 2)).toBe('1.50 KB')
    expect(fmtBytes('nope')).toBe('0 B')
  })

  it('converts between GB and bytes', () => {
    expect(gbToBytes(1)).toBe(1073741824)
    expect(gbToBytes('bad')).toBe(0)
    expect(bytesToGb(1073741824)).toBe(1)
    expect(bytesToGb(0)).toBe(0)
  })

  it('computes bounded percentages', () => {
    expect(pct(5, 10)).toBe(50)
    expect(pct(20, 10)).toBe(100)
    expect(pct(1, 0)).toBe(0)
    expect(ratioClass(95)).toBe('danger')
    expect(ratioClass(75)).toBe('warn')
    expect(ratioClass(10)).toBe('')
  })

  it('formats relative time without going negative for future dates', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T12:00:00Z'))
    expect(fmtRelative('2025-12-31T12:00:00Z')).toBe('1 天前')
    expect(fmtRelative('2026-01-01T11:59:30Z')).toBe('30 秒前')
    expect(fmtRelative('2026-01-01T12:01:00Z')).toBe('1 分钟后')
    expect(fmtRelative(null)).toBe('从未')
    expect(fmtRelative('not-a-date')).toBe('从未')
  })

  it('formats dates and short tokens', () => {
    expect(fmtDate(null)).toBe('—')
    expect(fmtDate('1969-12-31T00:00:00Z')).toBe('—')
    expect(shortToken('abcdefghijklmnopqrstuvwxyz')).toBe('abcdefghij…wxyz')
    expect(shortToken('short')).toBe('short')
    expect(shortToken('')).toBe('—')
  })
})
