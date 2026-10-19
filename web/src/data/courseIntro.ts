import type { Block } from '../types';

export function intro(p: string, section?: string): Block { return {type:'prose', html:p, section:section||undefined}; }
