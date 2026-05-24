import type { HostRecord } from './HostPicker';
import { recordKey } from './HostPicker';

/** Match saved run hosts against current search inventory. */
export function reconcileHosts(
  saved: HostRecord[],
  inventory: HostRecord[],
): { matched: HostRecord[]; missing: number; total: number } {
  if (!saved.length) {
    return { matched: [], missing: 0, total: 0 };
  }
  const byKey = new Map<string, HostRecord>();
  for (const r of inventory) {
    byKey.set(recordKey(r), r);
  }
  const matched: HostRecord[] = [];
  for (const h of saved) {
    const hit = byKey.get(recordKey(h));
    if (hit) {
      matched.push(hit);
    }
  }
  return { matched, missing: saved.length - matched.length, total: saved.length };
}
