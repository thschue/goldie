// §8: an error message is a sentence, then the hint is its own. When a hint follows, a message
// lacking a closing `.`, `?` or `!` gets a period; with no hint the message is unchanged (#759, #783).
export function errorSentence(message: string, hint?: string): string {
  return hint && !/[.?!]$/.test(message.trimEnd()) ? `${message.trimEnd()}.` : message;
}
