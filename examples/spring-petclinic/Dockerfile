# Builds spring-projects/spring-petclinic as the sample application for this
# module.
#
# The source is fetched rather than vendored: petclinic is a real upstream
# project and copying it here would mean maintaining a fork of it. The commit
# is pinned because petclinic's main branch carries a SNAPSHOT version, so an
# unpinned build is a different application every time and a failure could not
# be told apart from a change upstream.
#
#   docker build -t localhost:5001/spring-petclinic:v0.1.0 examples/spring-petclinic
#
# Pass --build-arg PETCLINIC_REF=<sha or tag> to build a different revision.

ARG JAVA_VERSION=17

FROM maven:3.9-eclipse-temurin-${JAVA_VERSION} AS build

# pinned to spring-projects/spring-petclinic main as of 2026-09-29
ARG PETCLINIC_REF=500158f732419217507c7656904b8e6aa1bcc0d6

ADD https://github.com/spring-projects/spring-petclinic/archive/${PETCLINIC_REF}.tar.gz /petclinic.tar.gz
RUN mkdir -p /src && tar -xzf /petclinic.tar.gz -C /src --strip-components=1

WORKDIR /src

# petclinic initialises its schema from application.properties, with
# spring.sql.init.mode=always, which has every replica run schema.sql on
# startup. The script is idempotent but not concurrency-safe: PostgreSQL's
# CREATE INDEX IF NOT EXISTS checks the catalog and then creates, so two
# replicas starting together both pass the check and the second dies with
# "duplicate key value violates unique constraint pg_class_relname_nsp_index".
#
# Flyway takes a database lock for the duration of a migration, so one replica
# applies them and the rest wait and find them already applied. The same SQL
# becomes V1 and V2 under db/migration/postgres, and the profile switches the
# startup initialiser off.
#
# This is kept as a patch over a pinned upstream rather than a fork of it: the
# change is three files and visible in this repo, and moving to a newer
# petclinic is a change to PETCLINIC_REF rather than a rebase of a fork.
COPY flyway/V1__schema.sql flyway/V2__seed_data.sql /src/src/main/resources/db/migration/postgres/
COPY flyway/application-postgres.properties /src/src/main/resources/application-postgres.properties
COPY flyway/dependencies.xml /tmp/flyway-dependencies.xml

# inserted after the project's own <dependencies>, which is the first one in
# the file; the parent POM manages the Flyway version, so none is given here
RUN awk '/<dependencies>/ && !inserted { \
      print; \
      while ((getline line < "/tmp/flyway-dependencies.xml") > 0) print line; \
      inserted = 1; \
      next \
    } { print }' pom.xml > pom.xml.new \
 && mv pom.xml.new pom.xml \
 && grep -q spring-boot-starter-flyway pom.xml

# Flyway is on by default once it is on the classpath. The default profile runs
# on H2 and keeps its own initialiser, so Flyway is turned off there and back
# on in the postgres profile.
RUN printf '\nspring.flyway.enabled=false\n' >> src/main/resources/application.properties

# the checks are the upstream project's own CI concern, not something to run on
# every image build: they add minutes and they fail for reasons that have
# nothing to do with whether this module can deploy the result
RUN mvn -B -DskipTests \
      -Dcheckstyle.skip=true \
      -Dspring-javaformat.skip=true \
      -Dspring-javaformat.validate.skip=true \
      -Dnohttp-checkstyle.skip=true \
      package

FROM eclipse-temurin:${JAVA_VERSION}-jre

# an unprivileged user: the module sets no securityContext, so the image is
# what decides, and petclinic needs nothing root can do
RUN useradd --create-home --shell /usr/sbin/nologin petclinic
USER petclinic
WORKDIR /home/petclinic

COPY --from=build --chown=petclinic:petclinic /src/target/spring-petclinic-*.jar app.jar

# 8080 is Spring Boot's default and what the module's ServerPort defaults to
EXPOSE 8080

# exec form so the JVM is PID 1 and receives the kubelet's TERM directly,
# rather than a shell swallowing it and leaving the pod to be killed on the
# grace period
ENTRYPOINT ["java", "-jar", "app.jar"]
