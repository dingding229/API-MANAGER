// New passwords follow the character bounds of the account forms.
export function validPassword(value:string):boolean{return [...value].length>=8&&value.length<=24}

// Optional configuration is serialized as null when no suffix policy is set.
export function normalizeEmailDomains(value: string[] | null | undefined): string[] {
  return Array.isArray(value) ? value : [];
}
