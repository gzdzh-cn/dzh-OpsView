// Completed estimates only; pending tasks and file contents never enter this cache.
// The authentication store clears it when the current session ends.
export const cachedDirectorySizes = new Map<string, number>()
