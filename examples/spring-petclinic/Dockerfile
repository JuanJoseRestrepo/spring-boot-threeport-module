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
