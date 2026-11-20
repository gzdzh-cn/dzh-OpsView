package model

// IDs and revisions come from the selected rows. Files, services and database
// relations are resolved again by the panel and the privileged executor.
type SiteDeleteItem struct {
	ID              string `json:"id"`
	Revision        int    `json:"revision"`
	DeleteDirectory bool   `json:"deleteDirectory"`
	DeleteDatabase  bool   `json:"deleteDatabase"`
	DeleteLogs      bool   `json:"deleteLogs"`
}
type SiteDeletePreview struct {
	Items       []SiteDeleteItem `json:"items"`
	Fingerprint string           `json:"fingerprint"`
	Session     string           `json:"session"`
	CreatedAt   int64            `json:"createdAt"`
}
