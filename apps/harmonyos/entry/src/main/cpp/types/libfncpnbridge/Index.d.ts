export const invoke: (action: number, input: string) => Promise<string>;
export const setSystem: (callback: (id: string, request: string) => void) => void;
export const reply: (id: string, response: string) => void;
