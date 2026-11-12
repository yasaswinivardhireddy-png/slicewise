import { describe, expect, it } from 'vitest';
import { chipsIn, clock, duration, shapeFits } from './format.ts';
import { summarize } from './live.tsx';

describe('format', () => {
  it('shows simulated time as a cluster clock', () => {
    expect(clock(0)).toBe('Day 1, 00:00');
    expect(clock(3600 + 5 * 60)).toBe('Day 1, 01:05');
    expect(clock(86_400 + 14 * 3600)).toBe('Day 2, 14:00');
  });

  it('shows lengths of time in words', () => {
    expect(duration(30)).toBe('under 1 min');
    expect(duration(45 * 60)).toBe('45 min');
    expect(duration(3600)).toBe('1 h');
    expect(duration(3900)).toBe('1 h 05 min');
    expect(duration(3 * 86_400)).toBe('3.0 days');
  });

  it('checks shapes against the pod in any rotation', () => {
    const pod = { x: 8, y: 8, z: 4 };
    expect(shapeFits('4x4x8', pod)).toBe(true);
    expect(shapeFits('8x8x8', pod)).toBe(false);
    expect(shapeFits('2x2', pod)).toBe(false);
    expect(chipsIn('2x4x4')).toBe(32);
  });
});

describe('live update summaries', () => {
  it('reads as one sentence', () => {
    expect(summarize({})).toBe('');
    expect(summarize({ started: 1 })).toBe('Update: 1 job started.');
    expect(summarize({ started: 2, succeeded: 1, preempted: 1 })).toBe('Update: 2 jobs started, 1 job preempted and 1 job finished.');
  });
});
