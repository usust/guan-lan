export function cpuOptions(cores: number): number[] {
  return Array.from({ length: Math.max(1, Math.floor(cores)) }, (_, i) => i + 1)
}
export const cpuLimits = cpuOptions(16)
// Stored in MiB; displayed in GB using the platform's 1024-based memory units.
export const memoryLimits = Array.from({ length: 32 }, (_, i) => (i + 1) * 512)
export const processLimits = [64, 128, 256, 512, 1024, 2048, 4096]

export function limitOptions(presets: number[], current: number): number[] {
  const finite = [...new Set([...presets, ...(current > 0 ? [current] : [])])].sort((a, b) => a - b)
  return [...finite, 0]
}
export function memoryGB(memoryMB: number): string {
  return `${Number((memoryMB / 1024).toFixed(4))} GB`
}
