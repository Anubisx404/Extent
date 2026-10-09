package stack

// Image is one container image used by the generated observability stack.
// Digest is optional; when set, the reference is pinned as repo:tag@digest.
type Image struct {
	Repository string
	Tag        string
	Digest     string
}

// Ref returns the image reference written into docker-compose.observability.yml.
func (i Image) Ref() string {
	ref := i.Repository + ":" + i.Tag
	if i.Digest != "" {
		ref += "@" + i.Digest
	}
	return ref
}

// Central table of every image the generated stack runs. Templates read from
// here only; bump versions in this one place.
var (
	ImageOTelCollector = Image{Repository: "otel/opentelemetry-collector-contrib", Tag: "0.162.0", Digest: "sha256:39923a8e431bd1f57be82411999d389fcfe40857492e4365456d97a4c1f74be6"}
	ImageGrafana       = Image{Repository: "grafana/grafana", Tag: "12.4.12", Digest: "sha256:83be3e511ede559bee80e2215bb1987cb1246075461cb961beb515b0341e7aea"}
	ImageTempo         = Image{Repository: "grafana/tempo", Tag: "2.9.5", Digest: "sha256:785fd6c81d7f26ff0973208cd7093ac6030e13946c288612ff32f02416941d3a"}
	ImageLoki          = Image{Repository: "grafana/loki", Tag: "3.7.8", Digest: "sha256:1107dd5274e0ada47e42472b7a7e71f3b2a2fe878878108f3e2f9e51528f0193"}
	ImagePrometheus    = Image{Repository: "prom/prometheus", Tag: "v3.15.0", Digest: "sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e"}
	ImageCAdvisor      = Image{Repository: "gcr.io/cadvisor/cadvisor", Tag: "v0.55.1", Digest: "sha256:3de2bd5203120b866d74a9b283b2ffb8ec382fbf9dc321814700c6ea6f44ec57"}
	ImageNodeExporter  = Image{Repository: "prom/node-exporter", Tag: "v1.12.1", Digest: "sha256:1b4e4438faca4dd7e001dd445d161a4a2091b0fededa84093b3a8dfeae1f1be0"}
)
