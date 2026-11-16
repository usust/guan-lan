import { expect, it } from 'vitest'
import { cpuOptions, cpuLimits, memoryLimits, processLimits, limitOptions, memoryGB } from '../src/resourceLimits'

it('offers whole CPU cores and common memory and process limits with unlimited last', () => {
  expect(cpuLimits.every(value => Number.isInteger(value) && value >= 1)).toBe(true)
  expect(cpuOptions(32)).toHaveLength(32)
  expect(cpuOptions(32).at(-1)).toBe(32)
  expect(memoryLimits[0]).toBe(512)
  expect(memoryLimits.map(memoryGB)).toContain('3 GB')
  expect(memoryLimits.map(memoryGB)).toContain('5 GB')
  expect(memoryLimits.every((value, i) => value === (i + 1) * 512)).toBe(true)
  for (const presets of [cpuLimits, memoryLimits, processLimits]) {
    const options = limitOptions(presets, 0)
    expect(options.at(-1)).toBe(0)
    expect(options.filter(value => value === 0)).toHaveLength(1)
  }
})
it('preserves saved custom limits without rounding them or duplicating stops', () => {
  expect(limitOptions(cpuLimits, 1.1)).toContain(1.1)
  expect(limitOptions(memoryLimits, 768)).toEqual([512, 768, ...memoryLimits.slice(1), 0])
  expect(limitOptions(cpuLimits, 2).filter(value => value === 2)).toHaveLength(1)
})
