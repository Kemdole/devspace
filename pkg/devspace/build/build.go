package build

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/devspace-cloud/devspace/pkg/devspace/config/generated"
	"github.com/devspace-cloud/devspace/pkg/devspace/config/versions/latest"
	"github.com/devspace-cloud/devspace/pkg/devspace/hook"
	"github.com/devspace-cloud/devspace/pkg/devspace/kubectl"
	logpkg "github.com/devspace-cloud/devspace/pkg/util/log"
	"github.com/devspace-cloud/devspace/pkg/util/randutil"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type imageNameAndTag struct {
	imageConfigName string
	imageName       string
	imageTag        string
}

// Options describe how images should be build
type Options struct {
	SkipPush                 bool
	IsDev                    bool
	ForceRebuild             bool
	Sequential               bool
	IgnoreContextPathChanges bool
}

// Controller is the main building interface
type Controller interface {
	Build(options *Options, log logpkg.Logger) (map[string]string, error)
}

type controller struct {
	config *latest.Config
	cache  *generated.CacheConfig

	hookExecuter hook.Executer
	client       kubectl.Client
}

// NewController creates a new image build controller
func NewController(config *latest.Config, cache *generated.CacheConfig, client kubectl.Client) Controller {
	return &controller{
		config: config,
		cache:  cache,

		hookExecuter: hook.NewExecuter(config),
		client:       client,
	}
}

// All builds all images
func (c *controller) Build(options *Options, log logpkg.Logger) (map[string]string, error) {
	var (
		builtImages = make(map[string]string)

		// Parallel build
		errChan   = make(chan error)
		cacheChan = make(chan imageNameAndTag)
	)

	// Check if we have at least 1 image to build
	if len(c.config.Images) == 0 {
		return builtImages, nil
	}

	// Build not in parallel when we only have one image to build
	if options.Sequential == false {
		// check if all images are disabled besides one
		imagesToBuild := 0
		for _, image := range c.config.Images {
			if image.Build == nil || image.Build.Disabled == nil || *image.Build.Disabled == false {
				imagesToBuild++
			}
		}
		if len(c.config.Images) <= 1 || imagesToBuild <= 1 {
			options.Sequential = true
		}
	}

	// Execute before images build hook
	err := c.hookExecuter.Execute(hook.Before, hook.StageImages, hook.All, hook.Context{Client: c.client, Config: c.config, Cache: c.cache}, log)
	if err != nil {
		return nil, err
	}

	imagesToBuild := 0
	for key, imageConf := range c.config.Images {
		if imageConf.Build != nil && imageConf.Build.Disabled != nil && *imageConf.Build.Disabled == true {
			log.Infof("Skipping building image %s", key)
			continue
		}

		// This is necessary for parallel build otherwise we would override the image conf pointer during the loop
		cImageConf := *imageConf
		imageName := cImageConf.Image
		imageConfigName := key

		// Execute before images build hook
		err = c.hookExecuter.Execute(hook.Before, hook.StageImages, imageConfigName, hook.Context{Client: c.client, Config: c.config, Cache: c.cache}, log)
		if err != nil {
			return nil, err
		}

		// Get image tags
		imageTags := []string{}
		if len(imageConf.Tags) > 0 {
			if imageConf.TagsAppendRandom {
				for _, t := range imageConf.Tags {
					r, err := randutil.GenerateRandomString(5)
					if err != nil {
						return nil, errors.Errorf("image building failed: %v", err)
					}

					imageTags = append(imageTags, t+"-"+r)
				}
			} else {
				imageTags = append(imageTags, imageConf.Tags...)
			}
		} else {
			imageTag, err := randutil.GenerateRandomString(7)
			if err != nil {
				return nil, errors.Errorf("image building failed: %v", err)
			}

			imageTags = append(imageTags, imageTag)
		}

		// replace the # in the tags
		for i := range imageTags {
			for strings.Contains(imageTags[i], "#") {
				r, err := randutil.GenerateRandomString(1)
				if err != nil {
					return nil, errors.Wrap(err, "generate random string")
				}

				imageTags[i] = strings.Replace(imageTags[i], "#", r, 1)
			}
		}

		// Create new builder
		builder, err := c.createBuilder(imageConfigName, &cImageConf, imageTags, options, log)
		if err != nil {
			return nil, errors.Wrap(err, "create builder")
		}

		// Check if rebuild is needed
		needRebuild, err := builder.ShouldRebuild(c.cache, options.ForceRebuild, options.IgnoreContextPathChanges)
		if err != nil {
			return nil, errors.Errorf("error during shouldRebuild check: %v", err)
		}

		if options.ForceRebuild == false && needRebuild == false {
			log.Infof("Skip building image '%s'", imageConfigName)
			continue
		}

		// Sequential or parallel build?
		if options.Sequential {
			// Build the image
			err = builder.Build(log)
			if err != nil {
				c.hookExecuter.OnError(hook.StageImages, []string{hook.All, imageConfigName}, hook.Context{Client: c.client, Config: c.config, Cache: c.cache, Error: err}, log)
				return nil, errors.Wrapf(err, "error building image %s:%s", imageName, imageTags[0])
			}

			// Update cache
			imageCache := c.cache.GetImageCache(imageConfigName)
			if imageCache.Tag == imageTags[0] {
				log.Warnf("Newly built image '%s' has the same tag as in the last build (%s), this can lead to problems that the image during deployment is not updated", imageName, imageTags[0])
			}

			imageCache.ImageName = imageName
			imageCache.Tag = imageTags[0]

			// Track built images
			builtImages[imageName] = imageTags[0]

			// Execute before images build hook
			err = c.hookExecuter.Execute(hook.After, hook.StageImages, imageConfigName, hook.Context{Client: c.client, Config: c.config, Cache: c.cache}, log)
			if err != nil {
				return nil, err
			}
		} else {
			imagesToBuild++
			go func() {
				// Create a string log
				buff := &bytes.Buffer{}
				streamLog := logpkg.NewStreamLogger(buff, logrus.InfoLevel)

				// Build the image
				err := builder.Build(streamLog)
				if err != nil {
					c.hookExecuter.OnError(hook.StageImages, []string{imageConfigName}, hook.Context{Client: c.client, Config: c.config, Cache: c.cache, Error: err}, log)
					errChan <- errors.Errorf("error building image %s:%s: %s %v", imageName, imageTags[0], buff.String(), err)
					return
				}

				// Execute before images build hook
				err = c.hookExecuter.Execute(hook.After, hook.StageImages, imageConfigName, hook.Context{Client: c.client, Config: c.config, Cache: c.cache}, log)
				if err != nil {
					errChan <- errors.Errorf("error executing image hook %s:%s: %v", imageName, imageTags[0], err)
					return
				}

				// Send the reponse
				cacheChan <- imageNameAndTag{
					imageConfigName: imageConfigName,
					imageName:       imageName,
					imageTag:        imageTags[0],
				}
			}()
		}
	}

	if options.Sequential == false && imagesToBuild > 0 {
		defer log.StopWait()

		for imagesToBuild > 0 {
			log.StartWait(fmt.Sprintf("Building %d images...", imagesToBuild))

			select {
			case err := <-errChan:
				c.hookExecuter.OnError(hook.StageImages, []string{hook.All}, hook.Context{Client: c.client, Config: c.config, Cache: c.cache, Error: err}, log)
				return nil, err
			case done := <-cacheChan:
				imagesToBuild--
				log.Donef("Done building image %s:%s (%s)", done.imageName, done.imageTag, done.imageConfigName)

				// Update cache
				imageCache := c.cache.GetImageCache(done.imageConfigName)
				if imageCache.Tag == done.imageTag {
					log.Warnf("Newly built image '%s' has the same tag as in the last build (%s), this can lead to problems that the image during deployment is not updated", done.imageName, done.imageTag)
				}

				imageCache.ImageName = done.imageName
				imageCache.Tag = done.imageTag

				// Track built images
				builtImages[done.imageName] = done.imageTag
			}
		}
	}

	// Execute after images build hook
	err = c.hookExecuter.Execute(hook.After, hook.StageImages, hook.All, hook.Context{Client: c.client, Config: c.config, Cache: c.cache}, log)
	if err != nil {
		return nil, err
	}

	return builtImages, nil
}
