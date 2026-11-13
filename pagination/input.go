package pagination

// Input is an embeddable request DTO for the default page limits. Tags are
// interpreted by the optional HTTP binding layer, not by pagination itself.
// Applications needing different limits should declare their own input type.
type Input struct {
	Current int64 `json:"current" d:"1" v:"gte=1" label:"页码"`
	Size    int64 `json:"size" d:"20" v:"gte=1,lte=100" label:"每页数量"`
}
