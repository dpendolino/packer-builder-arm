package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hashicorp/packer/packer"
)

// ShellCommand takes a command string and returns an *exec.Cmd to execute
// it within the context of a shell (/bin/sh)
func ShellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}

// Communicator is a special communicator that works by executing
// commands locally but within a chroot.
type Communicator struct {
	Chroot string
	Env    []string
}

// Start spawns and instance of exec.Command
func (c *Communicator) Start(ctx context.Context, cmd *packer.RemoteCmd) error {
	command := fmt.Sprintf("chroot %s /bin/sh -c \"%s\"", c.Chroot, cmd.Command)

	localCmd := ShellCommand(command)
	localCmd.Stdin = cmd.Stdin
	localCmd.Stdout = cmd.Stdout
	localCmd.Stderr = cmd.Stderr
	localCmd.Env = append(localCmd.Env, c.Env...)

	log.Printf("Executing: %s %#v", localCmd.Path, localCmd.Args)
	if err := localCmd.Start(); err != nil {
		return err
	}

	go func() {
		exitStatus := 0
		if err := localCmd.Wait(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitStatus = 1

				// There is no process-independent way to get the REAL
				// exit status so we just try to go deeper.
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					exitStatus = status.ExitStatus()
				}
			}
		}

		log.Printf(
			"Chroot execution exited with '%d': '%s'",
			exitStatus, cmd.Command)
		cmd.SetExited(exitStatus)
	}()

	return nil
}

// Upload copies files into chroot
func (c *Communicator) Upload(dst string, r io.Reader, fi *os.FileInfo) error {
	dst = filepath.Join(c.Chroot, dst)

	log.Printf("Uploading to chroot dir: %s", dst)

	tf, err := ioutil.TempFile("", "packer-arm-builder")
	if err != nil {
		return fmt.Errorf("Error preparing shell script: %s", err)
	}
	defer os.Remove(tf.Name())

	if _, err := io.Copy(tf, r); err != nil {
		return err
	}

	cpCmd := fmt.Sprintf("cp %s %s", tf.Name(), dst)

	return ShellCommand(cpCmd).Run()
}

// UploadDir copies directory into chroot
func (c *Communicator) UploadDir(dst string, src string, exclude []string) error {
	// If src ends with a trailing "/", copy from "src/." so that
	// directory contents (including hidden files) are copied, but the
	// directory "src" is omitted.  BSD does this automatically when
	// the source contains a trailing slash, but linux does not.
	if src[len(src)-1] == '/' {
		src = src + "."
	}

	// TODO: remove any file copied if it appears in `exclude`
	chrootDest := filepath.Join(c.Chroot, dst)

	log.Printf("Uploading directory '%s' to '%s'", src, chrootDest)
	cpCmd := fmt.Sprintf("cp -R '%s' %s", src, chrootDest)

	var stderr bytes.Buffer

	cmd := ShellCommand(cpCmd)
	cmd.Env = append(cmd.Env, "LANG=C")
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err == nil {
		return err
	}

	if strings.Contains(stderr.String(), "No such file") {
		// This just means that the directory was empty. Just ignore it.
		return nil
	}

	return nil
}

// DownloadDir N/A
func (c *Communicator) DownloadDir(src string, dst string, exclude []string) error {
	return fmt.Errorf("DownloadDir is not implemented for amazon-chroot")
}

// Download copies file from chroot
func (c *Communicator) Download(src string, w io.Writer) error {
	src = filepath.Join(c.Chroot, src)

	log.Printf("Downloading from chroot dir: %s", src)

	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(w, f); err != nil {
		return err
	}
	return nil
}
