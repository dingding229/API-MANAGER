// Match the server's bcrypt-compatible UTF-8 byte boundaries.
export function validPasswordBytes(value: string): boolean {
  const bytes = new TextEncoder().encode(value).length;
  return bytes >= 8 && bytes <= 72;
}
