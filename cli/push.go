package cli

import (
	"encoding/json"
	"github.com/blake-education/dogestry/remote"
	"github.com/blake-education/dogestry/utils"
	docker "github.com/fsouza/go-dockerclient"

	"archive/tar"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func (cli *DogestryCli) CmdPush(args ...string) error {
	cmd := cli.Subcmd("push", "REMOTE IMAGE[:TAG]", "push IMAGE to the REMOTE. TAG defaults to 'latest'")
	if err := cmd.Parse(args); err != nil {
		return nil
	}

	if len(cmd.Args()) < 2 {
		return fmt.Errorf("Error: IMAGE and REMOTE not specified")
	}

	remoteDef := cmd.Arg(0)
	image := cmd.Arg(1)

	imageRoot, err := cli.WorkDir(image)
	if err != nil {
		return err
	}

	remote, err := remote.NewRemote(remoteDef, cli.Config)
	if err != nil {
		return err
	}

	fmt.Printf("Remote: %v\n", remote.Desc())

	if err := cli.prepareImage(image, imageRoot); err != nil {
		return err
	}

	fmt.Println("pushing image to remote")
	if err := remote.Push(image, imageRoot); err != nil {
		return err
	}

	return nil
}

// Stream the tarball from docker and translate it into the portable repo format
// Note that its easier to handle as a stream on the way out.
func (cli *DogestryCli) prepareImage(image, root string) error {
	fmt.Printf("Preparing image (image: %v; root: %v)\n", image, root)

	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()

	tarball := tar.NewReader(reader)

	errch := make(chan error)

	go func() {
		// consume the tar
		for {
			header, err := tarball.Next()
			if err == io.EOF {
				break
			} // end of tar file

			if err != nil {
				errch <- err
				return
			}

			if err := cli.createFileFromTar(root, header, tarball); err != nil {
				errch <- err
				return
			}
		}

		errch <- nil
	}()

	if err := cli.client.ExportImage(docker.ExportImageOptions{image, writer}); err != nil {
		<-errch
		return err
	}

	// wait for the tar reader
	if err := <-errch; err != nil {
		return err
	}

	return nil
}

func (cli *DogestryCli) createFileFromTar(root string, header *tar.Header, tarball io.Reader) error {
	// only handle files (directories are implicit)
	if header.Typeflag == tar.TypeReg {
		fmt.Printf("  tar: processing %s\n", header.Name)

		// special case - repositories file
		if filepath.Base(header.Name) == "repositories" {
			if err := writeRepositories(root, tarball); err != nil {
				return err
			}

		} else {
			barename := strings.TrimPrefix(header.Name, "./")

			dest := filepath.Join(root, "images", barename)
			if err := os.MkdirAll(filepath.Dir(dest), os.ModeDir|0700); err != nil {
				return err
			}

			destFile, err := os.Create(dest)
			if err != nil {
				return err
			}

			if wrote, err := io.Copy(destFile, tarball); err != nil {
				return err
			} else {
				fmt.Printf("  tar: wrote %s\n", utils.HumanSize(wrote))
			}

			destFile.Close()
		}
	}

	return nil
}

type Repository map[string]string

func writeRepositories(root string, tarball io.Reader) error {
	destRoot := filepath.Join(root, "repositories")

	repositories := map[string]Repository{}
	if err := json.NewDecoder(tarball).Decode(&repositories); err != nil {
		return err
	}

	for repoName, repo := range repositories {
		for tag, id := range repo {
			dest := filepath.Join(destRoot, repoName, tag)

			if err := os.MkdirAll(filepath.Dir(dest), os.ModeDir|0700); err != nil {
				return err
			}

			if err := ioutil.WriteFile(dest, []byte(id), 0600); err != nil {
				return err
			}
		}
	}

	return nil
}
