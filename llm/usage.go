package llm

type Usage struct {
	InputTokens              int64 `json:"input_tokens,omitempty"`
	InputUncachedTokens      int64 `json:"input_uncached_tokens,omitempty"`
	InputCachedTokens        int64 `json:"input_cached_tokens,omitempty"`
	InputCacheCreationTokens int64 `json:"input_cache_creation_tokens,omitempty"`
	OutputTokens             int64 `json:"output_tokens,omitempty"`
	ReasoningTokens          int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens              int64 `json:"total_tokens,omitempty"`
}
