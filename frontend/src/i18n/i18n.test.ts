import { describe, expect, it } from 'vitest';
import en from './en.json';
import nl from './nl.json';
import { translate } from './index';

describe('message catalogs', () => {
  it('have the same keys in every language', () => {
    expect(Object.keys(nl).sort()).toEqual(Object.keys(en).sort());
  });

  it('keep the same placeholders in every language', () => {
    const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort();
    for (const [key, text] of Object.entries(en)) {
      expect(vars((nl as Record<string, string>)[key]), key).toEqual(vars(text));
    }
  });
});

describe('translate', () => {
  it('fills placeholders', () => {
    expect(translate('nl', 'home.helloName', { name: 'Sam' })).toBe('Hallo, Sam');
  });

  it('picks plural forms by count', () => {
    expect(translate('en', 'quiz.points', { count: 1 })).toBe('1 point');
    expect(translate('en', 'quiz.points', { count: 3 })).toBe('3 points');
    expect(translate('nl', 'quiz.points', { count: 0 })).toBe('0 punten');
  });

  it('leaves unknown placeholders alone', () => {
    expect(translate('en', 'due.on')).toBe('Due {date}');
  });
});
