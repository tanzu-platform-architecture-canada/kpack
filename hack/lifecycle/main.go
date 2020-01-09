package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/pivotal/kpack/pkg/cnb"
	"github.com/pivotal/kpack/pkg/registry"
	"github.com/pivotal/kpack/pkg/registry/imagehelpers"
)

const (
	lifecycleMetadataLabel = "io.buildpacks.lifecycle.metadata"
	lifecycleLocation      = "/cnb/lifecycle"
)

var (
	tag = flag.String("tag", "", "tag for lifecycle image")

	normalizedTime = time.Date(1980, time.January, 1, 0, 0, 1, 0, time.UTC)
)

func main() {
	flag.Parse()

	image, err := lifecycleImage(
		"https://github.com/buildpacks/lifecycle/releases/download/v0.5.0/lifecycle-v0.5.0+linux.x86-64.tgz",
		cnb.LifecycleMetadata{
			LifecycleInfo: cnb.LifecycleInfo{
				Version: "0.5.0",
			},
			API: cnb.LifecycleAPI{
				BuildpackVersion: "0.2",
				PlatformVersion:  "0.1",
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	identifier, err := (&registry.Client{}).Save(authn.DefaultKeychain, *tag, image)
	if err != nil {
		log.Fatal(err)
	}

	log.Println(fmt.Sprintf("saved lifecycle image: %s ", identifier))

}

func lifecycleImage(url string, lifecycleMetadata cnb.LifecycleMetadata) (v1.Image, error) {
	image, err := random.Image(0, 0)
	if err != nil {
		return nil, err
	}

	layer, err := lifecycleLayer(url)
	if err != nil {
		return nil, err
	}

	image, err = mutate.AppendLayers(image, layer)
	if err != nil {
		return nil, err
	}

	return imagehelpers.SetLabels(image, map[string]interface{}{
		lifecycleMetadataLabel: lifecycleMetadata,
	})
}

func lifecycleLayer(url string) (v1.Layer, error) {
	b := &bytes.Buffer{}
	tw := tar.NewWriter(b)

	var regex = regexp.MustCompile(`^[^/]+/([^/]+)$`)

	lr, err := lifecycleReader(url)
	if err != nil {
		return nil, err
	}
	defer lr.Close()
	tr := tar.NewReader(lr)
	for {
		header, err := tr.Next()
		if err != nil {
			break
		}

		pathMatches := regex.FindStringSubmatch(path.Clean(header.Name))
		if pathMatches != nil {
			binaryName := pathMatches[1]

			header.Name = lifecycleLocation + binaryName
			header.ModTime = normalizedTime
			err = tw.WriteHeader(header)
			if err != nil {
				return nil, err
			}

			buf, err := ioutil.ReadAll(tr)
			if err != nil {
				return nil, err
			}

			_, err = tw.Write(buf)
			if err != nil {
				return nil, err
			}
		}

	}

	return tarball.LayerFromReader(b)
}

func lifecycleReader(url string) (io.ReadCloser, error) {
	dir, err := ioutil.TempDir("", "lifecycle")
	if err != nil {
		return nil, err
	}

	lifecycleLocation := dir + "/lifecycle.tgz"

	err = download(lifecycleLocation, url)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(lifecycleLocation)
	if err != nil {
		return nil, err
	}

	gzr, err := gzip.NewReader(file)

	return &ReadCloserWrapper{
		Reader: gzr,
		closer: func() error {
			defer os.RemoveAll(dir)
			defer file.Close()
			return gzr.Close()
		},
	}, err
}

func download(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

//copied from github.com/docker/docker/pkg/ioutils
type ReadCloserWrapper struct {
	io.Reader
	closer func() error
}

func (r *ReadCloserWrapper) Close() error {
	return r.closer()
}
