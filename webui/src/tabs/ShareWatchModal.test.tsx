import { describe, it, expect } from 'vitest';
import { computeShareWatchScale } from './ShareWatchModal';

describe('computeShareWatchScale', () => {
  it('scales up to fill a container taller/wider than the content', () => {
    // A 201x57 guest window rendered small inside a much bigger modal: scale
    // up on the tighter axis so it fills the modal without cropping.
    expect(computeShareWatchScale(800, 420, 400, 300)).toBeCloseTo(1.4, 5);
  });

  it('scales down when the content is bigger than the container', () => {
    expect(computeShareWatchScale(400, 300, 800, 300)).toBeCloseTo(0.5, 5);
  });

  it('is a no-op (1) once container and content already match', () => {
    expect(computeShareWatchScale(400, 300, 400, 300)).toBe(1);
  });

  it('is a no-op for a not-yet-measurable (zero) box instead of Infinity/NaN', () => {
    expect(computeShareWatchScale(0, 0, 400, 300)).toBe(1);
    expect(computeShareWatchScale(800, 420, 0, 0)).toBe(1);
  });
});
