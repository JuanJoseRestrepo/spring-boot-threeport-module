package springboot

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	kube "github.com/threeport/threeport/pkg/kube/v0"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	api_v0 "spring-boot-threeport-module/pkg/api/v0"
)

const (
	// postgresImage is the database the module deploys when the definition asks
	// for one.
	postgresImage = "postgres:16-alpine"

	// postgresPort is the standard PostgreSQL port.
	postgresPort = 5432

	// dbName and dbUser name the database the application connects to. They are
	// fixed rather than configurable: the connection details travel to the app
	// through the same secret either way, so exposing them would add API
	// surface without giving the user anything.
	dbName = "springboot"
	dbUser = "springboot"
)

// DbSecretName returns the name of the Secret holding the database credentials
// for a definition. The instance reconciler creates it and the manifest
// references it, so both have to agree on the name.
func DbSecretName(definitionName string) string {
	return fmt.Sprintf("%s-db", definitionName)
}

// springBootManifestInput carries what the manifest needs to render.
//
// It is a struct rather than a parameter list because a Spring Boot definition
// has enough fields that a positional call would be a row of bare strings and
// ints, where transposing two of them still compiles.
type springBootManifestInput struct {
	definitionName string
	image          string
	profile        string
	serverPort     int
	javaOpts       string
	cpuRequest     string
	cpuLimit       string
	memoryRequest  string
	memoryLimit    string
	replicas       int
	environment    string
	database       string
	healthPath     string
	dbStorageGb    int
}

// springBootYaml returns a YAML manifest describing a Spring Boot application:
// the deployment and service, and, when the definition asks for one, a
// PostgreSQL database with its storage and credentials.
//
// The whole document is handed to Threeport as one Kubernetes workload
// definition, so the pieces are created and removed together.
//
// No namespace is set on any object: Threeport assigns one per workload
// instance and rewrites whatever the manifest declares, so naming it here would
// suggest a control the module does not have.
func springBootYaml(in springBootManifestInput) (string, error) {
	var yamlDoc string
	var err error

	labels := func(component string) map[string]interface{} {
		return map[string]interface{}{
			"app.kubernetes.io/name":       component,
			"app.kubernetes.io/instance":   in.definitionName,
			"app.kubernetes.io/managed-by": "spring-boot-threeport-module",
			"environment":                  in.environment,
		}
	}

	withDatabase := in.database == api_v0.DatabasePostgres
	dbSecretName := DbSecretName(in.definitionName)
	dbServiceName := fmt.Sprintf("%s-postgres", in.definitionName)

	// the secret these objects reference is deliberately not created here. It
	// holds a credential that has to differ per deployment, and this document
	// is rendered once per definition and shared by every instance of it, so a
	// secret built here would hand them all the same password. The instance
	// reconciler creates it in the instance's own namespace instead, which is
	// also what makes reusing one name across instances safe.

	if withDatabase {
		dbVolumeClaim := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "PersistentVolumeClaim",
				"metadata": map[string]interface{}{
					"name":   dbServiceName,
					"labels": labels("postgres"),
				},
				"spec": map[string]interface{}{
					"accessModes": []interface{}{"ReadWriteOnce"},
					"resources": map[string]interface{}{
						"requests": map[string]interface{}{
							"storage": fmt.Sprintf("%dGi", in.dbStorageGb),
						},
					},
				},
			},
		}
		yamlDoc, err = kube.AppendObjectToYamlDoc(dbVolumeClaim, yamlDoc)
		if err != nil {
			return yamlDoc, fmt.Errorf("failed to append database volume claim to YAML manifest: %w", err)
		}

		dbDeployment := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":   dbServiceName,
					"labels": labels("postgres"),
				},
				"spec": map[string]interface{}{
					// one replica only: this is a single volume with a single
					// writer, not a replicated database
					"replicas": 1,
					"selector": map[string]interface{}{
						"matchLabels": labels("postgres"),
					},
					"strategy": map[string]interface{}{
						// the volume cannot be mounted by two pods at once, so
						// the old pod has to go before the new one starts
						"type": "Recreate",
					},
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{
							"labels": labels("postgres"),
						},
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":  "postgres",
									"image": postgresImage,
									"ports": []interface{}{
										map[string]interface{}{"containerPort": postgresPort},
									},
									// named keys rather than envFrom: the
									// secret is read by two containers with
									// different needs, and envFrom would put
									// every key in both environments
									"env": []interface{}{
										secretEnv("POSTGRES_DB", dbSecretName, "POSTGRES_DB"),
										secretEnv("POSTGRES_USER", dbSecretName, "POSTGRES_USER"),
										secretEnv("POSTGRES_PASSWORD", dbSecretName, "POSTGRES_PASSWORD"),
									},
									"volumeMounts": []interface{}{
										map[string]interface{}{
											"name":      "data",
											"mountPath": "/var/lib/postgresql/data",
											// postgres refuses to initialise
											// into a directory that is not
											// empty, and a mounted volume root
											// carries lost+found
											"subPath": "pgdata",
										},
									},
									"readinessProbe": map[string]interface{}{
										"exec": map[string]interface{}{
											"command": []interface{}{"pg_isready", "-U", dbUser, "-d", dbName},
										},
										"initialDelaySeconds": 5,
										"periodSeconds":       5,
									},
								},
							},
							"volumes": []interface{}{
								map[string]interface{}{
									"name": "data",
									"persistentVolumeClaim": map[string]interface{}{
										"claimName": dbServiceName,
									},
								},
							},
						},
					},
				},
			},
		}
		yamlDoc, err = kube.AppendObjectToYamlDoc(dbDeployment, yamlDoc)
		if err != nil {
			return yamlDoc, fmt.Errorf("failed to append database deployment to YAML manifest: %w", err)
		}

		dbService := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":   dbServiceName,
					"labels": labels("postgres"),
				},
				"spec": map[string]interface{}{
					"selector": labels("postgres"),
					"ports": []interface{}{
						map[string]interface{}{
							"port":       postgresPort,
							"targetPort": postgresPort,
						},
					},
				},
			},
		}
		yamlDoc, err = kube.AppendObjectToYamlDoc(dbService, yamlDoc)
		if err != nil {
			return yamlDoc, fmt.Errorf("failed to append database service to YAML manifest: %w", err)
		}
	}

	// Spring Boot binds SPRING_DATASOURCE_* onto spring.datasource.*, and an
	// environment variable outranks anything in the image's property files, so
	// these win over whatever the application ships as its default. The URL is
	// a plain value: it names a Service, which is not a secret, and keeping it
	// out of the Secret means the credentials can be rotated without rewriting
	// a connection string.
	var appEnv []interface{}
	if in.profile != "" {
		appEnv = append(appEnv, map[string]interface{}{
			"name":  "SPRING_PROFILES_ACTIVE",
			"value": in.profile,
		})
	}
	if in.javaOpts != "" {
		// JAVA_TOOL_OPTIONS rather than JAVA_OPTS: the JVM reads it directly,
		// where JAVA_OPTS only does anything if the image's entrypoint happens
		// to pass it on, which a plain `java -jar` does not.
		appEnv = append(appEnv, map[string]interface{}{
			"name":  "JAVA_TOOL_OPTIONS",
			"value": in.javaOpts,
		})
	}
	if withDatabase {
		appEnv = append(appEnv,
			map[string]interface{}{
				"name": "SPRING_DATASOURCE_URL",
				"value": fmt.Sprintf(
					"jdbc:postgresql://%s:%d/%s",
					dbServiceName, postgresPort, dbName,
				),
			},
			secretEnv("SPRING_DATASOURCE_USERNAME", dbSecretName, "POSTGRES_USER"),
			secretEnv("SPRING_DATASOURCE_PASSWORD", dbSecretName, "POSTGRES_PASSWORD"),
		)
	}

	appContainer := map[string]interface{}{
		"name":  "spring-boot",
		"image": in.image,
		"ports": []interface{}{
			map[string]interface{}{"containerPort": in.serverPort},
		},
		// a JVM application takes long enough to start that a liveness probe
		// would restart it mid-boot and never let it finish. A startup probe
		// holds the other two off until the application answers once, so the
		// slow start is tolerated without also making a hung application take
		// five minutes to be noticed.
		"startupProbe": map[string]interface{}{
			"httpGet": map[string]interface{}{
				"path": in.healthPath,
				"port": in.serverPort,
			},
			"periodSeconds":    5,
			"failureThreshold": 60,
		},
		"readinessProbe": map[string]interface{}{
			"httpGet": map[string]interface{}{
				"path": in.healthPath,
				"port": in.serverPort,
			},
			"periodSeconds": 10,
		},
		"livenessProbe": map[string]interface{}{
			"httpGet": map[string]interface{}{
				"path": in.healthPath,
				"port": in.serverPort,
			},
			"periodSeconds":    20,
			"failureThreshold": 3,
		},
	}
	if len(appEnv) > 0 {
		appContainer["env"] = appEnv
	}
	// an empty resources block is not the same as none: the kube API accepts
	// it, but a LimitRange in the namespace would then have nothing to default
	// against the way it does when the field is absent
	if resources := containerResources(in); len(resources) > 0 {
		appContainer["resources"] = resources
	}

	appPodSpec := map[string]interface{}{
		"containers": []interface{}{appContainer},
	}
	if withDatabase {
		// the application and the database start together, so Spring would
		// otherwise open its connection pool against a database that is not
		// listening yet. Spring Boot fails the whole application context when
		// the datasource cannot be reached at startup, so this is a crash loop
		// rather than a retry.
		appPodSpec["initContainers"] = []interface{}{
			map[string]interface{}{
				"name":  "wait-for-database",
				"image": postgresImage,
				"command": []interface{}{
					"sh", "-c",
					fmt.Sprintf(
						"until pg_isready -h %s -p %d -U %s; do sleep 2; done",
						dbServiceName, postgresPort, dbUser,
					),
				},
			},
		}
	}

	appDeployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":   in.definitionName,
				"labels": labels("spring-boot"),
			},
			"spec": map[string]interface{}{
				"replicas": in.replicas,
				"selector": map[string]interface{}{
					"matchLabels": labels("spring-boot"),
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": labels("spring-boot"),
					},
					"spec": appPodSpec,
				},
			},
		},
	}
	yamlDoc, err = kube.AppendObjectToYamlDoc(appDeployment, yamlDoc)
	if err != nil {
		return yamlDoc, fmt.Errorf("failed to append application deployment to YAML manifest: %w", err)
	}

	appService := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":   in.definitionName,
				"labels": labels("spring-boot"),
			},
			"spec": map[string]interface{}{
				"selector": labels("spring-boot"),
				"ports": []interface{}{
					map[string]interface{}{
						"port":       80,
						"targetPort": in.serverPort,
					},
				},
			},
		},
	}
	yamlDoc, err = kube.AppendObjectToYamlDoc(appService, yamlDoc)
	if err != nil {
		return yamlDoc, fmt.Errorf("failed to append application service to YAML manifest: %w", err)
	}

	return yamlDoc, nil
}

// containerResources builds the application container's resources block from
// whichever of the four quantities were set, and returns nil when none were.
//
// Requests and limits are kept independent rather than defaulting one from the
// other. A request is what the scheduler reserves and a limit is what the
// kernel enforces, and for a JVM the limit is also what the heap is sized
// from, so inferring either from the other would be guessing at a number the
// application's owner chose deliberately.
func containerResources(in springBootManifestInput) map[string]interface{} {
	resources := map[string]interface{}{}
	for key, quantities := range map[string]map[string]string{
		"requests": {"cpu": in.cpuRequest, "memory": in.memoryRequest},
		"limits":   {"cpu": in.cpuLimit, "memory": in.memoryLimit},
	} {
		set := map[string]interface{}{}
		for name, quantity := range quantities {
			if quantity != "" {
				set[name] = quantity
			}
		}
		if len(set) > 0 {
			resources[key] = set
		}
	}
	if len(resources) == 0 {
		return nil
	}

	return resources
}

// secretEnv builds an environment variable sourced from a key in a Secret.
func secretEnv(name, secretName, key string) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"valueFrom": map[string]interface{}{
			"secretKeyRef": map[string]interface{}{
				"name": secretName,
				"key":  key,
			},
		},
	}
}

// generatePassword returns a URL-safe random password.
//
// crypto/rand rather than threeport's util.RandomAlphaNumericString: that
// helper seeds math/rand from the clock, so the value it produces is
// recoverable from the time the object was created. That is acceptable for a
// name suffix and not for a credential.
func generatePassword(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to read random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}
