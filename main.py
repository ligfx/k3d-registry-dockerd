import docker
import hashlib
import os
import tarfile
from flask import Flask, request, send_file

def docker_get_image_tarball(image_id):
    # get the raw response since Docker SDK turns it into an iterator
    res = client.api._get(client.api._url("/images/{0}/get", image_id), stream=True)
    client.api._raise_for_status(res)
    socket = client.api._get_raw_response_socket(res)
    client.api._disable_socket_timeout(socket)
    res.raw.decode_content = True

    class ForwardSeekableWrapper:
        def __init__(self, f):
            self._f = f
            self._p = 0
        def tell(self):
            return self._p
        def read(self, size=None):
            result = self._f.read(size)
            self._p += len(result)
            return result
        def seek(self, where):
            if where >= self._p:
                self.read(where - self._p)
                return
            raise NotImplementedError(self._p, where)
    
    return ForwardSeekableWrapper(res.raw)

app = Flask(__name__)
client = docker.from_env()

CACHE_DIRECTORY = '/var/lib/k3d-registry-dockerd/cache'
CACHE_BLOBS_SHA256_DIRECTORY = os.path.join(CACHE_DIRECTORY, 'blobs/sha256')
CACHE_INDEXES_DIRECTORY = os.path.join(CACHE_DIRECTORY, 'indexes')

@app.route("/")
def hello_world():
    return "Hello, world!"

@app.route("/v2/")
def v2():
    # return ('', 204)
    return "Hello, world!"

def find_cached_blob(sha256sum):
    cache_filename = os.path.join(CACHE_BLOBS_SHA256_DIRECTORY, sha256sum)
    if os.path.exists(cache_filename):
        return cache_filename

def find_cached_index(name):
    cache_filename = os.path.join(CACHE_INDEXES_DIRECTORY, name, 'index.json')
    if os.path.exists(cache_filename):
        return cache_filename

def save_oci_image_to_cache(name, tarfile_object):
    while True:
        ti = tarfile_object.next()
        if ti is None:
            break
        if ti.name.startswith('blobs/sha256/'):
            tf = tarfile_object.extractfile(ti)
            output_file = os.path.join(CACHE_BLOBS_SHA256_DIRECTORY, ti.name.replace('blobs/sha256/', ''))
            app.logger.info(f"writing {output_file}")
            os.makedirs(os.path.dirname(output_file), exist_ok=True)
            with open(output_file, 'wb') as f:
                while True:
                    chunk = tf.read(1024 * 1024)
                    if not chunk:
                        break
                    f.write(chunk)
        if ti.name == 'index.json':
            tf = tarfile_object.extractfile(ti)
            bdata = tf.read()
            m = hashlib.sha256()
            m.update(bdata)

            for output_file in [
                os.path.join(CACHE_INDEXES_DIRECTORY, name, 'index.json'),
                os.path.join(CACHE_BLOBS_SHA256_DIRECTORY, m.hexdigest())
            ]:
                app.logger.info(f"writing {output_file}")
                os.makedirs(os.path.dirname(output_file), exist_ok=True)
                with open(output_file, 'wb') as f:
                    f.write(bdata)


def find_blob(domain, namespace, image, reference, mimetype):
    if not domain:
        # don't support acting as own repo
        return ('', 404)
    
    if not reference.startswith('sha256:'):
        raise NotImplementedError("Bad reference %r" % reference)
    
    # pull from Docker's export cache, if it exists
    # TODO: pull from Docker's containerd storage (overlayfs?), if that storage engine is being used
    # cached = glob.glob(f'/var/lib/docker/tmp/docker-export-*/blobs/sha256/{reference.split("sha256:")[1]}')
    # if cached:
    #     app.logger.info(f'using cached file {cached[0]}')
    #     return send_file(cached[0], mimetype=mimetype if mimetype else 'application/octet-stream')

    # pull from our own cache, if it exists
    cached = find_cached_blob(reference.split("sha256:")[1])
    if cached:
        app.logger.info(f'using cached file {cached}')
        return send_file(cached, mimetype=mimetype if mimetype else 'application/octet-stream')

    # go pull some tarballs and take a look
    name=f'{domain}/{namespace}/{image}'
    if name.startswith('docker.io/'):
        name = name[len('docker.io/'):]
    # TODO: could probably be smarter and keep a cache of which blobs belong to which images, instead
    # of iterating all of them?
    for image_obj in client.images.list(name):
        t = tarfile.TarFile(fileobj=docker_get_image_tarball(image_obj.id))
        save_oci_image_to_cache(name, t)
        # TODO: remove old cached files if disk usage becomes too great

        cached = find_cached_blob(reference.split("sha256:")[1])
        if cached:
            app.logger.info(f'using cached file {cached}')
            return send_file(cached, mimetype=mimetype if mimetype else 'application/octet-stream')
                
    return ('No blob found', 404)

@app.route("/v2/<namespace>/<image>/blobs/<reference>")
def blobs(namespace, image, reference):
    if request.method != 'GET':
        raise NotImplementedError()

    domain = request.args.get('ns')
    app.logger.info(repr(request.headers))

    if not domain:
        # don't support acting as own repo
        return ("Requires a 'ns' query param", 500)

    mimetype = request.headers.get('accept').split(',')[0].strip()
    return find_blob(domain, namespace, image, reference, mimetype=mimetype)

@app.route("/v2/<namespace>/<image>/manifests/<reference>")
def manifests(namespace, image, reference):
    if request.method not in ('HEAD', 'GET'):
        raise ('', 405)

    app.logger.info(repr(request.headers))

    domain = request.args.get('ns')
    if not domain:
        # don't support acting as own repo
        return ('', 404)

    # TODO: check accept header

    if reference.startswith('sha256:'):
        mimetype = request.headers.get('accept').split(',')[0].strip()
        return find_blob(domain, namespace, image, reference, mimetype=mimetype)

    name=f'{domain}/{namespace}/{image}'
    if name.startswith('docker.io/'):
            name = name[len('docker.io/'):]
    cached = find_cached_index(name)
    if cached:
        app.logger.info(f'using cached file {cached}')
        return send_file(cached, mimetype='application/vnd.oci.image.index.v1+json')

    def find_image():
        for i in client.images.list(name):
            for t in i.tags:
                if t.count(":") != 1:
                    raise Exception(t)
                if t.split(":")[1] == reference:
                    return i
        return client.images.pull(name, tag=reference)

    image_obj = find_image()
    if not image_obj:
        return ('', 404)

    if request.method == 'HEAD':
        return ('', 200)

    # this tarfile is in OCI image format
    t = tarfile.TarFile(fileobj=docker_get_image_tarball(image_obj.id))
    save_oci_image_to_cache(name, t)

    cached = find_cached_index(name)
    if cached:
        app.logger.info(f'using cached file {cached}')
        return send_file(cached, mimetype='application/vnd.oci.image.index.v1+json')
    return ('No manifest found', 500)

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=True)